package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

// claudeSSEBufferBoundsFork bounds the entire event, including partial lines.
// A single data buffer avoids allocation overhead proportional to the line count.
type claudeSSEBufferBoundsFork struct {
	maxEventBytes int
	eventBytes    int
	bufferError   *interfaces.ErrorMessage
}

// preflightSSEChunkFork checks boundaries without changing the accepted parser
// state. Only a newline completing an existing partial line needs a temporary
// combined line; tiny chunks without newlines never copy the growing buffer.
func (s *claudeSSEStreamState) preflightSSEChunkFork(chunk []byte) bool {
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

func (s *claudeSSEStreamState) rejectSSEChunkFork() {
	// A boundary added before our terminal error can complete an already written
	// upstream error that lacked its blank line. Classify only those accepted
	// bytes once, so that error is not duplicated. No rejected bytes are parsed.
	s.FlushPendingLine()
	s.bufferError = &interfaces.ErrorMessage{
		StatusCode: http.StatusBadGateway,
		Error:      errors.New("upstream SSE event exceeds maximum size"),
	}
	s.lineBuf = bytes.Buffer{}
	s.eventData = bytes.Buffer{}
	s.currentEvent = ""
	s.eventBytes = 0
}

// acceptClaudeStreamChunkFork checks the first chunk before committing headers.
// Later chunks use ForwardStream.ChunkError to cancel after one terminal write.
func (h *ClaudeCodeAPIHandler) acceptClaudeStreamChunkFork(c *gin.Context, flusher http.Flusher, cancel func(error), state *claudeSSEStreamState, chunk []byte, committed bool) bool {
	state.ObserveChunk(chunk)
	if state.bufferError == nil {
		return true
	}
	if committed {
		h.writeClaudeStreamTerminalErrorFork(c.Writer, state, state.bufferError)
		flusher.Flush()
	} else {
		h.WriteErrorResponse(c, state.bufferError)
	}
	cancel(state.bufferError.Error)
	return false
}

func (h *ClaudeCodeAPIHandler) writeClaudeStreamTerminalErrorFork(w io.Writer, state *claudeSSEStreamState, errMsg *interfaces.ErrorMessage) {
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
