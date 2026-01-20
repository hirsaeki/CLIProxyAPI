package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
)

func TestIsAntigravityThinkingSignatureError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{
			name:       "valid antigravity thinking error",
			statusCode: 400,
			body:       `{"error":{"code":400,"message":"{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"messages.1.content.0: Invalid ` + "`signature`" + ` in ` + "`thinking`" + ` block\"},\"request_id\":\"req_123\"}","status":"INVALID_ARGUMENT"}}`,
			want:       true,
		},
		{
			name:       "direct anthropic format (fallback)",
			statusCode: 400,
			body:       `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid signature in thinking block"}}`,
			want:       true,
		},
		{
			name:       "non-400 status code",
			statusCode: 500,
			body:       `{"error":{"message":"Invalid signature in thinking block"}}`,
			want:       false,
		},
		{
			name:       "unrelated 400 error",
			statusCode: 400,
			body:       `{"error":{"code":400,"message":"Invalid request format","status":"INVALID_ARGUMENT"}}`,
			want:       false,
		},
		{
			name:       "rate limit error",
			statusCode: 429,
			body:       `{"error":{"code":429,"message":"Rate limit exceeded"}}`,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAntigravityThinkingSignatureError(tt.statusCode, []byte(tt.body))
			if got != tt.want {
				t.Errorf("isAntigravityThinkingSignatureError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractAntigravityInvalidThinkingPath(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantMsgIdx   int
		wantPartIdx  int
		wantOk       bool
	}{
		{
			name:         "valid path in nested JSON",
			body:         `{"error":{"code":400,"message":"{\"type\":\"error\",\"error\":{\"message\":\"messages.1.content.0: Invalid signature\"}}","status":"INVALID_ARGUMENT"}}`,
			wantMsgIdx:   1,
			wantPartIdx:  0,
			wantOk:       true,
		},
		{
			name:         "valid path in direct message",
			body:         `{"error":{"message":"messages.2.content.3: Invalid signature in thinking block"}}`,
			wantMsgIdx:   2,
			wantPartIdx:  3,
			wantOk:       true,
		},
		{
			name:         "no path in error",
			body:         `{"error":{"message":"Invalid request"}}`,
			wantMsgIdx:   0,
			wantPartIdx:  0,
			wantOk:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMsgIdx, gotPartIdx, gotOk := extractAntigravityInvalidThinkingPath([]byte(tt.body))
			if gotOk != tt.wantOk {
				t.Errorf("extractAntigravityInvalidThinkingPath() ok = %v, want %v", gotOk, tt.wantOk)
				return
			}
			if gotOk {
				if gotMsgIdx != tt.wantMsgIdx || gotPartIdx != tt.wantPartIdx {
					t.Errorf("extractAntigravityInvalidThinkingPath() = (%d, %d), want (%d, %d)",
						gotMsgIdx, gotPartIdx, tt.wantMsgIdx, tt.wantPartIdx)
				}
			}
		})
	}
}

func TestSanitizeAntigravityPayloadForInvalidThinking(t *testing.T) {
	sessionID := "test-session-antigravity"
	cache.ClearInvalidSignatureCache(sessionID)

	badSig := "bad-signature-1234567890123456789012345678901234567890123456"
	goodSig := "good-signature-123456789012345678901234567890123456789012345"

	// Cache the bad signature
	cache.CacheInvalidSignature(sessionID, badSig)

	payload := `{
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello"}]},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "bad thought", "signature": "` + badSig + `"},
				{"type": "thinking", "thinking": "good thought", "signature": "` + goodSig + `"},
				{"type": "text", "text": "response"}
			]}
		]
	}`

	sanitized, changed := sanitizeAntigravityPayloadForInvalidThinking([]byte(payload), sessionID)
	if !changed {
		t.Error("Expected payload to be changed")
	}

	sanitizedStr := string(sanitized)
	if !containsPlaceholder(sanitizedStr) {
		t.Error("Expected placeholder in sanitized payload")
	}

	// Good signature should still be present
	if !contains(sanitizedStr, goodSig) {
		t.Error("Good signature should not be removed")
	}
}

func TestStripAntigravityThinkingBlocksForRetry(t *testing.T) {
	payload := `{
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello"}]},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "thought1", "signature": "sig1"},
				{"type": "thinking", "thinking": "thought2", "signature": "sig2"},
				{"type": "text", "text": "response"}
			]}
		]
	}`

	stripped, changed := stripAntigravityThinkingBlocksForRetry([]byte(payload))
	if !changed {
		t.Error("Expected payload to be changed")
	}

	strippedStr := string(stripped)
	// Should not contain thinking blocks
	if contains(strippedStr, `"type":"thinking"`) {
		t.Error("Thinking blocks should be stripped")
	}
	// Should contain placeholder
	if !containsPlaceholder(strippedStr) {
		t.Error("Expected placeholder in stripped payload")
	}
}

func TestHandleAntigravityThinkingErrorRecovery(t *testing.T) {
	sessionID := "test-recovery-antigravity"
	cache.ClearInvalidSignatureCache(sessionID)

	sig := "test-sig-12345678901234567890123456789012345678901234567890"
	payload := `{
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello"}]},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "my thought", "signature": "` + sig + `"},
				{"type": "text", "text": "response"}
			]}
		]
	}`

	errBody := `{"error":{"code":400,"message":"{\"type\":\"error\",\"error\":{\"message\":\"messages.1.content.0: Invalid signature in thinking block\"}}","status":"INVALID_ARGUMENT"}}`

	newPayload, shouldRetry := handleAntigravityThinkingErrorRecovery(sessionID, []byte(payload), []byte(errBody), 400)
	if !shouldRetry {
		t.Error("Expected shouldRetry to be true")
	}

	// Signature should now be cached as invalid
	if !cache.IsInvalidSignature(sessionID, sig) {
		t.Error("Signature should be cached as invalid")
	}

	newPayloadStr := string(newPayload)
	if !containsPlaceholder(newPayloadStr) {
		t.Error("Expected placeholder in new payload")
	}
}

// Helper functions
func containsPlaceholder(s string) bool {
	return contains(s, "Previous thinking omitted")
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
