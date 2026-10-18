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

func TestClaudeSSEBufferRetentionFork(t *testing.T) {
	for _, size := range []int{1024, 64 * 1024, 128 * 1024} {
		var buffer bytes.Buffer
		buffer.Grow(size)
		buffer.WriteString("data: {}\n\n")
		capacity := buffer.Cap()
		ResetClaudeSSEBuffer(&buffer)
		if buffer.Len() != 0 {
			t.Fatal("reset retained event contents")
		}
		if capacity > 64*1024 {
			if buffer.Cap() != 0 {
				t.Fatalf("large event retained %d bytes", buffer.Cap())
			}
		} else if buffer.Cap() != capacity {
			t.Fatal("small reusable allocation was discarded")
		}
	}
}
