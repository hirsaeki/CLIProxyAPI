package helps

import (
	"bytes"
	"testing"
)

func TestClaudeSSEEventLimitFork(t *testing.T) {
	var event bytes.Buffer
	for i := 0; i < 4; i++ {
		if !appendClaudeSSELine(&event, []byte("abc"), 16) {
			t.Fatal("event at the exact byte limit was rejected")
		}
	}
	if appendClaudeSSELine(&event, nil, 16) || event.Len() != 16 {
		t.Fatalf("overflow must not append: size = %d", event.Len())
	}
	event.Reset()
	if !appendClaudeSSELine(&event, []byte("abcdefghijklmn"), 16) || !appendClaudeSSELine(&event, nil, 16) {
		t.Fatal("event reset or terminating newline was not accounted correctly")
	}
	event.Reset()
	if appendClaudeSSELine(&event, make([]byte, 16), 16) || event.Len() != 0 {
		t.Fatal("single oversized line was retained")
	}
}
