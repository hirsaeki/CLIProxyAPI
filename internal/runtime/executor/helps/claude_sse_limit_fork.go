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
