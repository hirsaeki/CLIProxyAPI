package helps

import "bytes"

// MaxClaudeSSEEventBytes also bounds multi-line events, not only scanner tokens.
const MaxClaudeSSEEventBytes = 50 * 1024 * 1024

// AppendClaudeSSELine rejects oversized events before retaining another line.
func AppendClaudeSSELine(event *bytes.Buffer, line []byte) bool {
	return appendClaudeSSELine(event, line, MaxClaudeSSEEventBytes)
}

func appendClaudeSSELine(event *bytes.Buffer, line []byte, limit int) bool {
	if event.Len() >= limit || len(line) >= limit-event.Len() {
		return false
	}
	event.Write(line)
	event.WriteByte('\n')
	return true
}

// ResetClaudeSSEBuffer avoids retaining a large event for an entire live stream.
// Keep small allocations reusable without keeping the maximum event capacity.
func ResetClaudeSSEBuffer(buffer *bytes.Buffer) {
	if buffer.Cap() > 64*1024 {
		*buffer = bytes.Buffer{}
		return
	}
	buffer.Reset()
}
