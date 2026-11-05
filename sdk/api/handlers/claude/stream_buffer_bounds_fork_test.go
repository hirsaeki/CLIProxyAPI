package claude

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

func claudeBoundedStateFork(limit int) *claudeSSEStreamStateFork {
	return &claudeSSEStreamStateFork{
		claudeSSEBufferBoundsFork: claudeSSEBufferBoundsFork{maxEventBytes: limit},
		claudeSSEStreamState:      claudeSSEStreamState{trailingNewlines: 2},
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
				if state.lineBuf.Len()+state.boundedEventData.Len() > limit {
					t.Fatalf("retained payload exceeds budget: line=%d data=%d", state.lineBuf.Len(), state.boundedEventData.Len())
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
			if state.lineBuf.Cap() != 0 || state.boundedEventData.Cap() != 0 || state.currentEvent != "" {
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
			} else if recorder.Code != http.StatusBadGateway || util.GetGJSONBytesNoCopy(recorder.Body.Bytes(), "error.type").String() != "api_error" {
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
			if !state.sawErrorEvent || state.sawMessageStop || state.lineBuf.Cap() != 0 || state.boundedEventData.Cap() != 0 {
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
	data := bytes.Clone(state.boundedEventData.Bytes())
	count := state.eventBytes
	chunk := []byte("\x80\x83\ndata: y\n")
	original := bytes.Clone(chunk)
	if !state.preflightSSEChunkFork(chunk) {
		t.Fatal("valid preflight was rejected")
	}
	if !bytes.Equal(chunk, original) || !bytes.Equal(state.lineBuf.Bytes(), partial) || !bytes.Equal(state.boundedEventData.Bytes(), data) || state.eventBytes != count {
		t.Fatal("preflight modified an input buffer or accepted parser state")
	}
}

func TestClaudeSSEBufferRetentionFork(t *testing.T) {
	const retainLimit = 64 * 1024
	largeLine := []byte(`data: {"type":"ping","padding":"` + strings.Repeat("x", retainLimit+1) + `"}`)
	for _, atEOF := range []bool{false, true} {
		name := "event boundary"
		if atEOF {
			name = "pending line at EOF"
		}
		t.Run(name, func(t *testing.T) {
			state := claudeBoundedStateFork(len(largeLine) + 2)
			// Retain an incomplete large line across chunks until it can be parsed.
			state.ObserveChunk(largeLine[:len(largeLine)/2])
			state.ObserveChunk(largeLine[len(largeLine)/2:])
			if state.bufferError != nil || state.lineBuf.Len() != len(largeLine) || state.lineBuf.Cap() <= retainLimit {
				t.Fatalf("large partial line was not retained: error=%v len=%d cap=%d", state.bufferError, state.lineBuf.Len(), state.lineBuf.Cap())
			}
			if atEOF {
				state.FlushPendingLine()
			} else {
				state.ObserveChunk([]byte("\n"))
				if state.lineBuf.Cap() != 0 || state.boundedEventData.Len() <= retainLimit {
					t.Fatalf("complete line storage was not released or pending event lost: line cap=%d data len=%d", state.lineBuf.Cap(), state.boundedEventData.Len())
				}
				state.ObserveChunk([]byte("\n"))
			}
			if state.bufferError != nil || state.lineBuf.Len() != 0 || state.boundedEventData.Len() != 0 || state.lineBuf.Cap() > retainLimit || state.boundedEventData.Cap() != 0 {
				t.Fatalf("large completed event retained storage: error=%v line len/cap=%d/%d data len/cap=%d/%d", state.bufferError, state.lineBuf.Len(), state.lineBuf.Cap(), state.boundedEventData.Len(), state.boundedEventData.Cap())
			}
		})
	}

	t.Run("small buffers are reused", func(t *testing.T) {
		state := claudeBoundedStateFork(1024)
		line := []byte(`data: {"type":"ping"}`)
		state.ObserveChunk(line)
		lineStorage, lineCapacity := &state.lineBuf.Bytes()[0], state.lineBuf.Cap()
		state.ObserveChunk([]byte("\n"))
		dataStorage, dataCapacity := &state.boundedEventData.Bytes()[0], state.boundedEventData.Cap()
		state.ObserveChunk([]byte("\n"))
		if state.lineBuf.Cap() != lineCapacity || state.boundedEventData.Cap() != dataCapacity {
			t.Fatal("reset released a small reusable buffer")
		}
		state.ObserveChunk(line)
		if &state.lineBuf.Bytes()[0] != lineStorage {
			t.Fatal("small line buffer was reallocated")
		}
		state.ObserveChunk([]byte("\n"))
		if &state.boundedEventData.Bytes()[0] != dataStorage {
			t.Fatal("small event data buffer was reallocated")
		}
		state.ObserveChunk([]byte("\n"))
		if state.bufferError != nil || state.lineBuf.Cap() != lineCapacity || state.boundedEventData.Cap() != dataCapacity {
			t.Fatal("small buffer reuse changed parsing or retained capacity")
		}
	})
}

// The upstream slice is deliberately retained for merge compatibility, but only
// the fork-owned bounded buffer may collect event data. Check each incremental
// byte so a future upstream writer cannot hide behind a later boundary reset.
func TestClaudeSSEUpstreamEventSliceRemainsUnusedFork(t *testing.T) {
	assertUnused := func(t *testing.T, state *claudeSSEStreamStateFork) {
		t.Helper()
		if len(state.eventData) != 0 || cap(state.eventData) != 0 {
			t.Fatalf("upstream event slice acquired storage: len=%d cap=%d", len(state.eventData), cap(state.eventData))
		}
	}
	observeBytes := func(t *testing.T, state *claudeSSEStreamStateFork, text string) {
		t.Helper()
		for i := range len(text) {
			state.ObserveChunk([]byte(text[i : i+1]))
			assertUnused(t, state)
		}
	}
	t.Run("multiline and boundary", func(t *testing.T) {
		state := claudeBoundedStateFork(256)
		observeBytes(t, state, "event: error\ndata: {\"type\":\ndata: \"error\",\"error\":{}}\n")
		if state.bufferError != nil || state.sawErrorEvent || state.boundedEventData.Len() == 0 {
			t.Fatal("accepted multiline event was not held in the bounded buffer")
		}
		observeBytes(t, state, "\n")
		if state.bufferError != nil || !state.sawErrorEvent || state.boundedEventData.Len() != 0 {
			t.Fatal("multiline event was not classified and cleared at its boundary")
		}
		observeBytes(t, state, "data: {\"type\":\"message_stop\"}\n\n")
		if !state.sawMessageStop || state.bufferError != nil {
			t.Fatal("following event did not reuse the bounded path")
		}
		state.FlushPendingLine()
		assertUnused(t, state)
	})
	t.Run("flush pending partial line", func(t *testing.T) {
		state := claudeBoundedStateFork(256)
		observeBytes(t, state, "data: {\"type\":\"message_stop\"}")
		if state.sawMessageStop || state.bufferError != nil {
			t.Fatal("partial line was prematurely classified")
		}
		state.FlushPendingLine()
		assertUnused(t, state)
		if !state.sawMessageStop || state.lineBuf.Len() != 0 || state.boundedEventData.Len() != 0 {
			t.Fatal("pending line was not classified and cleared on flush")
		}
	})
	t.Run("reject and sticky flush", func(t *testing.T) {
		state := claudeBoundedStateFork(64)
		observeBytes(t, state, "data: {\"type\":")
		state.ObserveChunk([]byte(strings.Repeat("x", 65)))
		assertUnused(t, state)
		if state.bufferError == nil || state.sawErrorEvent || state.sawMessageStop || state.boundedEventData.Cap() != 0 {
			t.Fatal("overflow did not reject and release the pending bounded event")
		}
		originalError := state.bufferError
		observeBytes(t, state, "\n\ndata: {\"type\":\"message_stop\"}\n\n")
		state.FlushPendingLine()
		assertUnused(t, state)
		if state.bufferError != originalError || state.sawMessageStop || state.lineBuf.Cap() != 0 || state.boundedEventData.Cap() != 0 {
			t.Fatal("rejected state accepted or retained subsequent data")
		}
	})
}

func TestClaudeSSEFirstChunkPreflightDoesNotObserveFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})
	state := claudeBoundedStateFork(128)
	chunk := []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	if !handler.acceptClaudeStreamChunkFork(c, c.Writer, func(error) {
		t.Fatal("accepted chunk canceled the request")
	}, state, chunk, false) {
		t.Fatal("valid first chunk was rejected")
	}
	if state.sawMessageStop || state.eventBytes != 0 || state.lineBuf.Len() != 0 || state.boundedEventData.Len() != 0 || c.Writer.Written() {
		t.Fatal("preflight parsed data or committed the response")
	}
	state.ObserveChunk(chunk)
	if !state.sawMessageStop || state.bufferError != nil || len(state.eventData) != 0 {
		t.Fatal("accepted chunk did not reach the bounded upstream observer")
	}
}

func TestClaudeSSEBoundedOptionsPreserveCallbacksFork(t *testing.T) {
	state := claudeBoundedStateFork(64)
	fallback := &interfaces.ErrorMessage{StatusCode: http.StatusTeapot, Error: errors.New("callback sentinel")}
	writes, terminalWrites := 0, 0
	opts := boundClaudeStreamOptionsFork(state, handlers.StreamForwardOptions{
		WriteChunk: func(chunk []byte) {
			writes++
			if state.lineBuf.Len() != 0 {
				t.Fatal("wrapper observed accepted bytes before the upstream callback")
			}
			state.ObserveChunk(chunk)
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			terminalWrites++
			if state.bufferError == nil || errMsg != state.bufferError || state.sawErrorEvent {
				t.Fatal("overflow bypassed the original terminal callback or preempted its state")
			}
		},
		ChunkError: func() *interfaces.ErrorMessage { return fallback },
		CloseError: func() *interfaces.ErrorMessage { return fallback },
	})
	opts.WriteChunk([]byte("data: {\"partial\":"))
	if writes != 1 || terminalWrites != 0 || opts.ChunkError() != fallback || opts.CloseError() != fallback {
		t.Fatal("accepted chunk or existing error callbacks were not preserved")
	}
	opts.WriteChunk([]byte(strings.Repeat("x", 65)))
	opts.WriteChunk([]byte("discarded after overflow"))
	if writes != 1 || terminalWrites != 1 || state.bufferError == nil || opts.ChunkError() != state.bufferError || opts.CloseError() != state.bufferError {
		t.Fatal("overflow did not delegate once and retain its cancellation error")
	}
}

// Compare all upstream state fields, including fields added by later syncs, while
// ignoring buffer storage. The fork must delegate JSON classification unchanged.
func TestClaudeSSEBoundedObserverMatchesUpstreamFork(t *testing.T) {
	for _, payload := range []string{
		"data: {\"type\":\"message_stop\"}\n\n",
		"event: error\ndata: {\"error\":{\"message\":\"failed\"}}\n\n",
		"data: {\"type\":\"message_delta\",\n" +
			"data: \"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":{\"type\":\"refusal\",\"category\":\"cyber\"}}}\n\n",
		"{\"type\":\"message\",\"stop_reason\":\"end_turn\"}\n",
	} {
		for split := 0; split <= len(payload); split++ {
			state := claudeBoundedStateFork(1024)
			upstream := claudeSSEStreamState{trailingNewlines: 2}
			for _, chunk := range []string{payload[:split], payload[split:]} {
				state.ObserveChunk([]byte(chunk))
				upstream.ObserveChunk([]byte(chunk))
				if len(state.eventData) != 0 || cap(state.eventData) != 0 {
					t.Fatal("bounded observer used the unbounded upstream event slice")
				}
			}
			state.FlushPendingLine()
			upstream.FlushPendingLine()
			actual := state.claudeSSEStreamState
			actual.lineBuf, upstream.lineBuf = bytes.Buffer{}, bytes.Buffer{}
			actual.eventData, upstream.eventData = nil, nil
			if state.bufferError != nil || !reflect.DeepEqual(actual, upstream) {
				t.Fatalf("bounded/upstream observer mismatch at split %d for %q: got %+v want %+v", split, payload, actual, upstream)
			}
		}
	}
}
