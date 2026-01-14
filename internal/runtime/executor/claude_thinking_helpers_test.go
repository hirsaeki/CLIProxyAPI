package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
)

func TestIsClaudeThinkingInvalidSignatureError(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   bool
	}{
		{400, `{"error":{"message":"Invalid signature in thinking block"}}`, true},
		{400, `{"message":"invalid signature error at thinking..."}`, true},
		{400, `Invalid signature in thinking block`, true},
		{401, `Invalid signature in thinking block`, false},
		{400, `Some unrelated error`, false},
	}

	for _, tt := range tests {
		if got := isClaudeThinkingInvalidSignatureError(tt.status, []byte(tt.body)); got != tt.want {
			t.Errorf("isClaudeThinkingInvalidSignatureError(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}

func TestExtractInvalidThinkingPath(t *testing.T) {
	body := `{"error":{"message":"messages.1.content.0: Invalid signature in thinking block"}}`
	mi, pi, ok := extractInvalidThinkingPath([]byte(body))
	if !ok || mi != 1 || pi != 0 {
		t.Errorf("extractInvalidThinkingPath failed: %v, %d, %d", ok, mi, pi)
	}
}

func TestSanitizeClaudePayloadForInvalidThinking(t *testing.T) {
	sessionID := "test-session"
	sig := "bad-sig"
	cache.CacheInvalidSignature(sessionID, sig)
	defer cache.ClearInvalidSignatureCache(sessionID)

	payload := `{
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "...", "signature": "bad-sig"},
				{"type": "text", "text": "OK"}
			]}
		]
	}`

	sanitized, changed := sanitizeClaudePayloadForInvalidThinking([]byte(payload), sessionID)
	if !changed {
		t.Error("Expected payload to be changed")
	}

	thinkingType := gjson.GetBytes(sanitized, "messages.1.content.0.type").String()
	if thinkingType != "text" {
		t.Errorf("Expected thinking block to be converted to text, got %s", thinkingType)
	}

	text := gjson.GetBytes(sanitized, "messages.1.content.0.text").String()
	if text != "[Previous thinking omitted due to invalid signature]" {
		t.Errorf("Unexpected placeholder text: %s", text)
	}
}

func TestStripClaudeThinkingBlocksForRetry(t *testing.T) {
	payload := `{
		"messages": [
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "thought"},
				{"type": "text", "text": "result"}
			]}
		]
	}`

	stripped, changed := stripClaudeThinkingBlocksForRetry([]byte(payload))
	if !changed {
		t.Error("Expected payload to be changed")
	}

	thinkingType := gjson.GetBytes(stripped, "messages.0.content.0.type").String()
	if thinkingType != "text" {
		t.Errorf("Expected thinking block to be converted to text, got %s", thinkingType)
	}
}
