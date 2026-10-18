package claude

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

func claudeBoundedStateFork(limit int) *claudeSSEStreamState {
	return &claudeSSEStreamState{
		claudeSSEBufferBoundsFork: claudeSSEBufferBoundsFork{maxEventBytes: limit},
		trailingNewlines:          2,
	}
}

func TestClaudeSSEBufferLimitsFork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chunk string
	}{
		{name: "many data lines", chunk: "data: x\n"},
		{name: "many comment lines", chunk: ": x\n"},
		{name: "many event headers", chunk: "event: x\n"},
		{name: "unterminated line across chunks", chunk: "xxxxxxxx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const limit = 64
			state := claudeBoundedStateFork(limit)
			for i := 0; i < limit+1 && state.bufferError == nil; i++ {
				state.ObserveChunk([]byte(tc.chunk))
				if state.lineBuf.Len()+state.eventData.Len() > limit {
					t.Fatalf("retained payload exceeds budget: line=%d data=%d", state.lineBuf.Len(), state.eventData.Len())
				}
			}
			if state.bufferError == nil || state.bufferError.StatusCode != http.StatusBadGateway {
				t.Fatalf("expected sticky 502 overflow, got %v", state.bufferError)
			}
			originalError := state.bufferError
			state.ObserveChunk([]byte("\n\ndata: {\"type\":\"message_stop\"}\n\n"))
			state.FlushPendingLine()
			if state.bufferError != originalError || state.sawMessageStop || state.sawErrorEvent {
				t.Fatal("overflow was cleared or a discarded terminal event was observed")
			}
			if state.lineBuf.Cap() != 0 || state.eventData.Cap() != 0 || state.currentEvent != "" {
				t.Fatal("overflow retained its buffers")
			}
		})
	}
}

func TestClaudeSSEBufferBoundaryAndResetFork(t *testing.T) {
	const event = "data: {\"type\":\"message_stop\"}\n\n"
	state := claudeBoundedStateFork(len(event))
	state.ObserveChunk([]byte(event[:len(event)-1]))
	if state.sawMessageStop {
		t.Fatal("partial event was accepted before its boundary")
	}
	state.ObserveChunk([]byte(event[len(event)-1:]))
	if state.bufferError != nil || !state.sawMessageStop || state.eventBytes != 0 {
		t.Fatalf("exactly-at-limit event failed: error=%v bytes=%d", state.bufferError, state.eventBytes)
	}
	for i := 0; i < 3; i++ {
		state.ObserveChunk([]byte(event))
	}
	if state.bufferError != nil {
		t.Fatal("separate events did not reset their budget")
	}

	state = claudeBoundedStateFork(64)
	state.ObserveChunk([]byte(strings.Repeat("x", 64)))
	if state.bufferError != nil || state.lineBuf.Len() != 64 {
		t.Fatal("exactly-at-limit partial line was rejected")
	}
	state.ObserveChunk([]byte("x"))
	if state.bufferError == nil {
		t.Fatal("the first over-limit byte was accepted")
	}
}

func TestClaudeSSERejectedChunkDoesNotCommitTerminalStateFork(t *testing.T) {
	for _, terminal := range []string{
		"data: {\"type\":\"error\",\"error\":{}}\n\n",
		"data: {\"type\":\"message_stop\"}\n\n",
		"{\"type\":\"message\",\"stop_reason\":\"end_turn\"}\n\n",
	} {
		state := claudeBoundedStateFork(64)
		state.trailingNewlines = 1
		state.ObserveChunk([]byte(terminal + strings.Repeat("x", 65) + "\n\n"))
		if state.bufferError == nil {
			t.Fatal("oversized chunk did not fail")
		}
		if state.sawErrorEvent || state.sawMessageStop || state.sawBareMessageJSON || state.trailingNewlines != 1 {
			t.Fatal("a withheld chunk changed delivered terminal/newline state")
		}
	}
}

func TestClaudeStreamOverflowCancelsOnceBeforeChannelCloseFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"data: {\"partial\":", "data: {\"partial\":\n"} {
		t.Run(strings.ReplaceAll(prefix, "\n", "newline"), func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
			c.Header("Content-Type", "text/event-stream")
			handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})
			state := claudeBoundedStateFork(64)
			data := make(chan []byte, 2)
			data <- []byte(prefix)
			data <- []byte(strings.Repeat("REJECTED", 10) + "\n\ndata: {\"type\":\"message_stop\"}\n\n")
			// Leave the source open: overflow must finish without waiting for EOF.
			var cancellations int
			var canceledWith error
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.forwardClaudeStreamWithState(c, c.Writer, func(err error) {
					cancellations++
					canceledWith = err
				}, data, nil, state)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				stop()
				<-done
				t.Fatal("overflow waited for source closure")
			}
			if cancellations != 1 || canceledWith == nil || state.bufferError == nil || canceledWith != state.bufferError.Error {
				t.Fatalf("cancel count=%d error=%v", cancellations, canceledWith)
			}
			body := recorder.Body.String()
			separator := "\n\n"
			if strings.HasSuffix(prefix, "\n") {
				separator = "\n"
			}
			if !strings.HasPrefix(body, prefix+separator+"event: error\ndata: ") {
				t.Fatalf("terminal error lacks a boundary after the written prefix: %q", body)
			}
			if strings.Count(body, "event: error") != 1 || strings.Contains(body, "REJECTED") || strings.Contains(body, "message_stop") || state.sawMessageStop {
				t.Fatalf("overflow leaked data, duplicated errors, or completed: %q", body)
			}
		})
	}
}

func TestClaudeStreamFirstChunkOverflowFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, committed := range []bool{false, true} {
		name := "before headers"
		if committed {
			name = "after keepalive"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})
			state := claudeBoundedStateFork(64)
			if committed {
				c.Header("Content-Type", "text/event-stream")
				_, _ = c.Writer.Write([]byte(": keep-alive\n\n"))
				c.Writer.Flush()
			}
			cancellations := 0
			accepted := handler.acceptClaudeStreamChunkFork(c, c.Writer, func(err error) {
				cancellations++
				if err == nil {
					t.Error("overflow cancellation had no error")
				}
			}, state, []byte(strings.Repeat("REJECTED", 10)+"\n\n"), committed)
			if accepted || cancellations != 1 || strings.Contains(recorder.Body.String(), "REJECTED") {
				t.Fatalf("accepted=%t cancellations=%d body=%q", accepted, cancellations, recorder.Body.String())
			}
			if committed {
				if recorder.Code != http.StatusOK || strings.Count(recorder.Body.String(), "event: error") != 1 {
					t.Fatalf("committed overflow response = %d %q", recorder.Code, recorder.Body.String())
				}
			} else if recorder.Code != http.StatusBadGateway || gjson.GetBytes(recorder.Body.Bytes(), "error.type").String() != "api_error" {
				t.Fatalf("uncommitted overflow response = %d %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestClaudeStreamOverflowPreservesPendingErrorFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, newline := range []string{"", "\n"} {
		name := "without newline"
		if newline != "" {
			name = "with one newline"
		}
		t.Run(name, func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(ctx)
			c.Header("Content-Type", "text/event-stream")
			handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})
			state := claudeBoundedStateFork(128)
			prefix := "event: error\ndata: {\"type\":\"error\",\"error\":{}}" + newline
			data := make(chan []byte, 2)
			data <- []byte(prefix)
			data <- []byte(strings.Repeat("REJECTED", 20) + "\n\n")
			var cancellations int
			var canceledWith error
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.forwardClaudeStreamWithState(c, c.Writer, func(err error) {
					cancellations++
					canceledWith = err
				}, data, nil, state)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				stop()
				<-done
				t.Fatal("overflow waited for source closure")
			}
			if cancellations != 1 || state.bufferError == nil || canceledWith != state.bufferError.Error {
				t.Fatalf("cancel count=%d error=%v", cancellations, canceledWith)
			}
			want := strings.TrimSuffix(prefix, "\n") + "\n\n"
			if got := recorder.Body.String(); got != want {
				t.Fatalf("pending upstream error was duplicated or rejected bytes leaked: got %q want %q", got, want)
			}
			if !state.sawErrorEvent || state.sawMessageStop || state.lineBuf.Cap() != 0 || state.eventData.Cap() != 0 {
				t.Fatal("pending error classification or overflow cleanup failed")
			}
		})
	}
}

func TestClaudeSSEPreflightSplitWhitespaceBoundaryFork(t *testing.T) {
	state := claudeBoundedStateFork(16)
	// U+2003 is whitespace, even when its UTF-8 bytes straddle chunks.
	state.ObserveChunk([]byte("data: x\n\xe2"))
	state.ObserveChunk([]byte("\x80\x83\n1234567890123456"))
	if state.bufferError != nil || state.eventBytes != 16 || state.lineBuf.Len() != 16 {
		t.Fatalf("split whitespace failed to reset the event budget: error=%v bytes=%d", state.bufferError, state.eventBytes)
	}
	state.ObserveChunk([]byte("x"))
	if state.bufferError == nil {
		t.Fatal("over-limit byte after the split boundary was accepted")
	}
}

func TestClaudeSSEPreflightKeepsInputBuffersFork(t *testing.T) {
	state := claudeBoundedStateFork(64)
	state.ObserveChunk([]byte("data: x\n\xe2"))
	partial := bytes.Clone(state.lineBuf.Bytes())
	data := bytes.Clone(state.eventData.Bytes())
	count := state.eventBytes
	chunk := []byte("\x80\x83\ndata: y\n")
	original := bytes.Clone(chunk)
	if !state.preflightSSEChunkFork(chunk) {
		t.Fatal("valid preflight was rejected")
	}
	if !bytes.Equal(chunk, original) || !bytes.Equal(state.lineBuf.Bytes(), partial) || !bytes.Equal(state.eventData.Bytes(), data) || state.eventBytes != count {
		t.Fatal("preflight modified an input buffer or accepted parser state")
	}
}
