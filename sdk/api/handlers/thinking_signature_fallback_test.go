package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
)

func TestShouldFallbackThinkingSignature(t *testing.T) {
	tests := []struct {
		name string
		err  *interfaces.ErrorMessage
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "nil underlying error",
			err:  &interfaces.ErrorMessage{StatusCode: 400, Error: nil},
			want: false,
		},
		{
			name: "non-400 status",
			err:  &interfaces.ErrorMessage{StatusCode: 500, Error: errors.New("Invalid signature in thinking block")},
			want: false,
		},
		{
			name: "plain text - thinking signature error",
			err:  &interfaces.ErrorMessage{StatusCode: 400, Error: errors.New("Invalid signature in thinking block")},
			want: true,
		},
		{
			name: "plain text - case insensitive",
			err:  &interfaces.ErrorMessage{StatusCode: 400, Error: errors.New("INVALID SIGNATURE IN THINKING BLOCK")},
			want: true,
		},
		{
			name: "plain text - other 400 error",
			err:  &interfaces.ErrorMessage{StatusCode: 400, Error: errors.New("model not found")},
			want: false,
		},
		{
			name: "JSON - standard Claude error",
			err: &interfaces.ErrorMessage{
				StatusCode: 400,
				Error:      errors.New(`{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid signature in thinking block"}}`),
			},
			want: true,
		},
		{
			name: "JSON - OpenAI-style error",
			err: &interfaces.ErrorMessage{
				StatusCode: 400,
				Error:      errors.New(`{"error":{"message":"Invalid signature in thinking block"}}`),
			},
			want: true,
		},
		{
			name: "JSON - nested Antigravity error",
			err: &interfaces.ErrorMessage{
				StatusCode: 400,
				Error:      errors.New(`{"error":{"code":400,"message":"{\"type\":\"error\",\"error\":{\"message\":\"Invalid signature in thinking block\"}}","status":"INVALID_ARGUMENT"}}`),
			},
			want: true,
		},
		{
			name: "JSON - unrelated error",
			err: &interfaces.ErrorMessage{
				StatusCode: 400,
				Error:      errors.New(`{"error":{"message":"rate limit exceeded"}}`),
			},
			want: false,
		},
		{
			name: "JSON - partial match missing thinking",
			err: &interfaces.ErrorMessage{
				StatusCode: 400,
				Error:      errors.New(`{"error":{"message":"Invalid signature"}}`),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldFallbackThinkingSignature(tt.err)
			if got != tt.want {
				errStr := "<nil>"
				if tt.err != nil && tt.err.Error != nil {
					errStr = tt.err.Error.Error()
				}
				t.Errorf("ShouldFallbackThinkingSignature() = %v, want %v, errText=%s", got, tt.want, errStr)
			}
		})
	}
}

func TestIsThinkingSignatureErrorText(t *testing.T) {
	tests := []struct {
		name    string
		errText string
		want    bool
	}{
		{"empty", "", false},
		{"plain text match", "Invalid signature in thinking block", true},
		{"lowercase match", "invalid signature in thinking block", true},
		{"uppercase match", "INVALID SIGNATURE IN THINKING BLOCK", true},
		{"with position prefix", "messages.1.content.0: Invalid signature in thinking block", true},
		{"partial - no thinking", "Invalid signature", false},
		{"partial - no signature", "Invalid thinking block", false},
		{"partial - no invalid", "signature in thinking block", false},
		{"unrelated error", "model not found", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isThinkingSignatureErrorText(tt.errText)
			if got != tt.want {
				t.Errorf("isThinkingSignatureErrorText(%q) = %v, want %v", tt.errText, got, tt.want)
			}
		})
	}
}

func TestCloneRequestWithoutStream(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKey  string
		wantBool bool
	}{
		{
			name:     "stream true to false",
			input:    `{"model":"test","stream":true}`,
			wantKey:  "stream",
			wantBool: false,
		},
		{
			name:     "no stream key",
			input:    `{"model":"test"}`,
			wantKey:  "stream",
			wantBool: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CloneRequestWithoutStream([]byte(tt.input))
			if string(result) == "" {
				t.Fatal("result is empty")
			}
			// Parse and check stream is false
			if val := gjson.GetBytes(result, "stream"); val.Bool() != tt.wantBool {
				t.Errorf("stream = %v, want %v", val.Bool(), tt.wantBool)
			}
		})
	}
}

func TestBuildOpenAIFinalOnlySSE(t *testing.T) {
	nonStreamResp := `{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hello!"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	result := BuildOpenAIFinalOnlySSE([]byte(nonStreamResp))
	resultStr := string(result)

	if !strings.HasPrefix(resultStr, "data: ") {
		t.Error("result should start with 'data: '")
	}
	if !strings.HasSuffix(resultStr, "data: [DONE]\n\n") {
		t.Error("result should end with 'data: [DONE]\\n\\n'")
	}
	if !strings.Contains(resultStr, "chat.completion.chunk") {
		t.Error("result should contain 'chat.completion.chunk'")
	}
	if !strings.Contains(resultStr, "chatcmpl-123") {
		t.Error("result should preserve id")
	}
	if !strings.Contains(resultStr, "Hello!") {
		t.Error("result should contain content")
	}
}

func TestBuildClaudeFinalOnlySSE(t *testing.T) {
	nonStreamResp := `{
		"id": "msg_123",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Hello world"}
		],
		"model": "claude-3-5-sonnet-20241022",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`

	result := BuildClaudeFinalOnlySSE([]byte(nonStreamResp))
	resultStr := string(result)

	if !strings.Contains(resultStr, "event: message_start") {
		t.Error("result should contain message_start event")
	}
	if !strings.Contains(resultStr, "event: content_block_start") {
		t.Error("result should contain content_block_start event")
	}
	if !strings.Contains(resultStr, "event: content_block_delta") {
		t.Error("result should contain content_block_delta event")
	}
	if !strings.Contains(resultStr, "event: content_block_stop") {
		t.Error("result should contain content_block_stop event")
	}
	if !strings.Contains(resultStr, "event: message_stop") {
		t.Error("result should contain message_stop event")
	}
	if !strings.Contains(resultStr, "Hello world") {
		t.Error("result should contain content text")
	}
}

func TestBuildGeminiFinalOnlySSE(t *testing.T) {
	nonStreamResp := `{"candidates":[{"content":{"parts":[{"text":"Hi"}]}}]}`

	t.Run("SSE mode (alt empty)", func(t *testing.T) {
		result := BuildGeminiFinalOnlySSE([]byte(nonStreamResp), "")
		resultStr := string(result)
		if !strings.HasPrefix(resultStr, "data: ") {
			t.Error("SSE mode should start with 'data: '")
		}
		if !strings.HasSuffix(resultStr, "\n\n") {
			t.Error("SSE mode should end with newlines")
		}
	})

	t.Run("raw mode (alt non-empty)", func(t *testing.T) {
		result := BuildGeminiFinalOnlySSE([]byte(nonStreamResp), "json")
		if string(result) != nonStreamResp {
			t.Error("raw mode should return original JSON")
		}
	})
}
