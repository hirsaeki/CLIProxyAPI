package common

import "bytes"

// SSEEventPayload extracts data from one SSE event while leaving bare payloads
// untouched. The caller owns event boundaries; this is not a stream deframer.
// Metadata-only events are SSE with no payload and should not be forwarded.
func SSEEventPayload(raw []byte) (payload []byte, isSSE bool) {
	for _, prefix := range []string{
		"data:", "event:", "id:", "retry:", ":",
		"data\n", "event\n", "id\n", "retry\n",
		"data\r\n", "event\r\n", "id\r\n", "retry\r\n",
	} {
		if bytes.HasPrefix(raw, []byte(prefix)) {
			isSSE = true
			break
		}
	}
	if !isSSE {
		return raw, false
	}
	var data [][]byte
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			break
		}
		if bytes.Equal(line, []byte("data")) {
			data = append(data, nil)
		} else if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimPrefix(line[5:], []byte{' '}))
		}
	}
	// Match the same-format translators' existing trimming of data payloads.
	return bytes.TrimSpace(bytes.Join(data, []byte{'\n'})), true
}
