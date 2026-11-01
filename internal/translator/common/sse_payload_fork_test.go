package common

import "testing"

func TestSSEEventPayloadFork(t *testing.T) {
	for _, tt := range []struct {
		name, input, want string
		isSSE             bool
	}{
		{"bare JSON", "\n {\"id\":1}\n", "\n {\"id\":1}\n", false},
		{"bare text", "identity\nretrying", "identity\nretrying", false},
		{"single line", "data: {\"id\":1}", "{\"id\":1}", true},
		{"metadata before data", "id: 1\nretry: 1000\nevent: message\n: comment\ndata: {}\n\n", "{}", true},
		{"metadata after data", "data: {}\nid: 1\nretry: 1000\n\n", "{}", true},
		{"multiline", "retry: 1000\r\ndata: {\r\ndata:  \"id\":1\r\ndata: }\r\n\r\n", "{\n \"id\":1\n}", true},
		{"empty data line", "id: 1\ndata: first\ndata\ndata: last\n\n", "first\n\nlast", true},
		{"colonless LF", "id\nretry\nevent\ndata: {}\n\n", "{}", true},
		{"colonless CRLF", "id\r\nretry\r\nevent\r\ndata: {}\r\n\r\n", "{}", true},
		{"colonless data", "data\ndata: {}\n\n", "{}", true},
		{"metadata only", "id: 1\nretry: 1000\n\n", "", true},
		{"comment only", ": keepalive\n\n", "", true},
		{"terminal", "retry: 1000\ndata: [DONE]\n\n", "[DONE]", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, isSSE := SSEEventPayload([]byte(tt.input))
			if string(got) != tt.want || isSSE != tt.isSSE {
				t.Fatalf("SSEEventPayload = (%q, %t), want (%q, %t)", got, isSSE, tt.want, tt.isSSE)
			}
		})
	}
}
