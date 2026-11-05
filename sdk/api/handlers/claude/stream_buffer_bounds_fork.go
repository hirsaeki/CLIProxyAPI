package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

// claudeSSEStreamStateFork owns bounded storage while retaining the upstream
// state and JSON observer, including future classification and logging fields.
type claudeSSEStreamStateFork struct {
	claudeSSEStreamState
	claudeSSEBufferBoundsFork

	// A synchronous callback may consume the exact preflighted chunk once.
	// The callback must not mutate the bytes or parser state before observation.
	preflightChunkFork []byte
}

// claudeSSEBufferBoundsFork bounds the entire event, including partial lines.
// A single data buffer avoids allocation overhead proportional to the line count.
type claudeSSEBufferBoundsFork struct {
	boundedEventData bytes.Buffer
	maxEventBytes    int
	eventBytes       int
	bufferError      *interfaces.ErrorMessage
}

// preflightSSEChunkFork checks boundaries without changing the accepted parser
// state. Only a newline completing an existing partial line needs a temporary
// combined line; tiny chunks without newlines never copy the growing buffer.
func (s *claudeSSEStreamStateFork) preflightSSEChunkFork(chunk []byte) bool {
	limit := s.maxEventBytes
	if limit <= 0 {
		limit = helps.MaxClaudeSSEEventBytes
	}
	eventBytes := s.eventBytes
	partialLine := s.lineBuf.Bytes()
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		size := len(chunk)
		if newline >= 0 {
			size = newline + 1
		}
		if size > limit-eventBytes {
			return false
		}
		eventBytes += size
		if newline < 0 {
			return true
		}
		line := chunk[:newline]
		if len(partialLine) > 0 {
			combined := make([]byte, 0, len(partialLine)+len(line))
			combined = append(combined, partialLine...)
			combined = append(combined, line...)
			line = combined
		}
		if len(bytes.TrimSpace(line)) == 0 {
			eventBytes = 0
		}
		partialLine = nil
		chunk = chunk[size:]
	}
	return true
}

func (s *claudeSSEStreamStateFork) rejectSSEChunkFork() {
	s.preflightChunkFork = nil
	// A boundary added before our terminal error can complete an already written
	// upstream error that lacked its blank line. Classify only those accepted
	// bytes once, so that error is not duplicated. No rejected bytes are parsed.
	s.FlushPendingLine()
	s.bufferError = &interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      errors.New("upstream SSE event exceeds maximum size"),
	}
	s.lineBuf = bytes.Buffer{}
	s.boundedEventData = bytes.Buffer{}
	s.currentEvent = ""
	s.eventBytes = 0
}

// acceptClaudeStreamChunkFork preflights the first chunk before committing headers.
// Accepted chunks remain unobserved until the upstream first-chunk path parses them.
func (h *ClaudeCodeAPIHandler) acceptClaudeStreamChunkFork(c *gin.Context, flusher http.Flusher, cancel func(error), state *claudeSSEStreamStateFork, chunk []byte, committed bool) bool {
	if state == nil {
		state = &claudeSSEStreamStateFork{claudeSSEStreamState: claudeSSEStreamState{trailingNewlines: 2}}
	}
	if state.bufferError == nil && state.preflightSSEChunkFork(chunk) {
		state.preflightChunkFork = chunk
		return true
	}
	if state.bufferError == nil {
		state.rejectSSEChunkFork()
	}
	if committed {
		h.LoggingAPIResponseError(context.WithValue(c, "gin", c), state.bufferError)
		h.writeClaudeStreamTerminalErrorFork(c.Writer, state, state.bufferError)
		flusher.Flush()
	} else {
		h.WriteErrorResponse(c, state.bufferError)
	}
	cancel(state.bufferError.Error)
	return false
}

func (h *ClaudeCodeAPIHandler) writeClaudeStreamTerminalErrorFork(w io.Writer, state *claudeSSEStreamStateFork, errMsg *interfaces.ErrorMessage) {
	state.FlushPendingLine()
	if errMsg == nil {
		return
	}
	if state.sawErrorEvent {
		state.ensureSSEEventBoundary(w)
		return
	}
	state.ensureSSEEventBoundary(w)
	errorBytes, _ := json.Marshal(h.toClaudeError(errMsg))
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", errorBytes)
	state.sawErrorEvent = true
}

// resetLineBufferFork keeps retained line storage bounded independently of parsing.
func (s *claudeSSEStreamStateFork) resetLineBufferFork() {
	helps.ResetClaudeSSEBuffer(&s.lineBuf)
}

// flushBoundedEventDataFork owns the fork buffer; the upstream event slice stays empty.
func (s *claudeSSEStreamStateFork) flushBoundedEventDataFork() {
	if s.boundedEventData.Len() > 0 {
		joined := bytes.TrimSpace(s.boundedEventData.Bytes())
		if len(joined) > 0 && json.Valid(joined) {
			s.observeJSONPayload(joined, true)
		}
		helps.ResetClaudeSSEBuffer(&s.boundedEventData)
	}
}

// boundClaudeStreamOptionsFork keeps size rejection outside the upstream parsing
// and terminal-error callbacks, so their classification and logging stay intact.
func boundClaudeStreamOptionsFork(state *claudeSSEStreamStateFork, opts handlers.StreamForwardOptions) handlers.StreamForwardOptions {
	writeChunk := opts.WriteChunk
	writeTerminalError := opts.WriteTerminalError
	chunkError := opts.ChunkError
	closeError := opts.CloseError
	opts.WriteChunk = func(chunk []byte) {
		if state.bufferError != nil {
			return
		}
		if !state.preflightSSEChunkFork(chunk) {
			state.rejectSSEChunkFork()
			if writeTerminalError != nil {
				writeTerminalError(state.bufferError)
				state.sawErrorEvent = true
			}
			return
		}
		if writeChunk != nil {
			state.preflightChunkFork = chunk
			defer func() { state.preflightChunkFork = nil }()
			writeChunk(chunk)
		}
	}
	opts.ChunkError = func() *interfaces.ErrorMessage {
		if state.bufferError != nil {
			return state.bufferError
		}
		if chunkError != nil {
			return chunkError()
		}
		return nil
	}
	opts.CloseError = func() *interfaces.ErrorMessage {
		if state.bufferError != nil {
			return state.bufferError
		}
		if closeError != nil {
			return closeError()
		}
		return nil
	}
	return opts
}

func (s *claudeSSEStreamStateFork) ObserveChunk(chunk []byte) {
	preflighted := s.preflightChunkFork
	s.preflightChunkFork = nil
	if len(chunk) == 0 || s.bufferError != nil {
		return
	}
	// Only reuse validation for the exact slice passed to the synchronous callback.
	// Other direct observations retain their own preflight before parsing.
	alreadyChecked := len(preflighted) == len(chunk) && len(preflighted) > 0 && &preflighted[0] == &chunk[0]
	if !alreadyChecked && !s.preflightSSEChunkFork(chunk) {
		s.rejectSSEChunkFork()
		return
	}
	for _, b := range chunk {
		s.eventBytes++
		if b == '\n' {
			s.observeLine(s.lineBuf.Bytes())
			s.resetLineBufferFork()
			continue
		}
		s.lineBuf.WriteByte(b)
	}
	s.trailingNewlines = advanceClaudeSSENewlineCount(s.trailingNewlines, chunk)
}

func (s *claudeSSEStreamStateFork) FlushPendingLine() {
	s.preflightChunkFork = nil
	if s.bufferError != nil {
		return
	}
	if s.lineBuf.Len() > 0 {
		s.observeLine(s.lineBuf.Bytes())
		s.resetLineBufferFork()
	}
	s.flushEventData()
}

func (s *claudeSSEStreamStateFork) observeLine(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		s.flushEventData()
		s.currentEvent = ""
		s.eventBytes = 0
		return
	}
	if bytes.HasPrefix(trimmed, []byte("event:")) {
		s.currentEvent = strings.TrimSpace(string(trimmed[len("event:"):]))
		return
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		payload := bytes.TrimSpace(trimmed[len("data:"):])
		if len(payload) > 0 {
			if s.boundedEventData.Len() > 0 {
				s.boundedEventData.WriteByte('\n')
			}
			s.boundedEventData.Write(payload)
		}
		return
	}
	if !bytes.HasPrefix(trimmed, []byte("{")) || !ojson.ValidBytes(trimmed) {
		return
	}
	s.observeJSONPayload(trimmed, false)
}

func (s *claudeSSEStreamStateFork) flushEventData() {
	s.flushBoundedEventDataFork()
}
