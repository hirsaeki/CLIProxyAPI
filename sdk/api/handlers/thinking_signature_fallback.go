// Package handlers provides core API handler functionality for the CLI Proxy API server.
package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ShouldFallbackThinkingSignature returns true if the error is a thinking signature
// error that qualifies for streaming fallback (retry with non-stream mode).
//
// Conditions:
//   - err != nil
//   - err.StatusCode == 400
//   - error text contains "invalid" + "signature" + "thinking" (case-insensitive)
func ShouldFallbackThinkingSignature(err *interfaces.ErrorMessage) bool {
	if err == nil || err.StatusCode != 400 || err.Error == nil {
		return false
	}
	return isThinkingSignatureErrorText(err.Error.Error())
}

// isThinkingSignatureErrorText checks if the error text contains the thinking signature error pattern.
// It handles both JSON error payloads and plain text.
func isThinkingSignatureErrorText(errText string) bool {
	if errText == "" {
		return false
	}

	msgLower := strings.ToLower(errText)
	if containsThinkingSignaturePattern(msgLower) {
		return true
	}

	if json.Valid([]byte(errText)) {
		if msg := gjson.Get(errText, "error.message").String(); msg != "" {
			if containsThinkingSignaturePattern(strings.ToLower(msg)) {
				return true
			}
			if json.Valid([]byte(msg)) {
				nestedMsg := gjson.Get(msg, "error.message").String()
				if containsThinkingSignaturePattern(strings.ToLower(nestedMsg)) {
					return true
				}
			}
		}
		if msg := gjson.Get(errText, "message").String(); msg != "" {
			if containsThinkingSignaturePattern(strings.ToLower(msg)) {
				return true
			}
		}
	}

	return false
}

// containsThinkingSignaturePattern checks for the signature error keywords.
func containsThinkingSignaturePattern(msgLower string) bool {
	return strings.Contains(msgLower, "invalid") &&
		strings.Contains(msgLower, "signature") &&
		strings.Contains(msgLower, "thinking")
}

// CloneRequestWithoutStream clones the raw JSON request and sets stream=false.
func CloneRequestWithoutStream(rawJSON []byte) []byte {
	result, _ := sjson.SetBytes(rawJSON, "stream", false)
	return result
}

// BuildOpenAIFinalOnlySSE builds a final-only SSE response for OpenAI chat completions
// from a non-streaming response body. Returns SSE formatted bytes ready to write.
func BuildOpenAIFinalOnlySSE(nonStreamResp []byte) []byte {
	root := gjson.ParseBytes(nonStreamResp)

	id := root.Get("id").String()
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	model := root.Get("model").String()
	created := root.Get("created").Int()
	if created == 0 {
		created = time.Now().Unix()
	}

	chunk := `{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[]}`
	chunk, _ = sjson.Set(chunk, "id", id)
	chunk, _ = sjson.Set(chunk, "created", created)
	chunk, _ = sjson.Set(chunk, "model", model)

	choices := root.Get("choices").Array()
	for i, choice := range choices {
		idx := choice.Get("index").Int()
		if !choice.Get("index").Exists() {
			idx = int64(i)
		}
		finishReason := choice.Get("finish_reason").String()
		message := choice.Get("message")

		delta := `{}`
		if role := message.Get("role").String(); role != "" {
			delta, _ = sjson.Set(delta, "role", role)
		}
		if content := message.Get("content").String(); content != "" {
			delta, _ = sjson.Set(delta, "content", content)
		}
		if toolCalls := message.Get("tool_calls").Raw; toolCalls != "" {
			delta, _ = sjson.SetRaw(delta, "tool_calls", toolCalls)
		}

		choiceJSON := `{"index":0,"delta":{}}`
		choiceJSON, _ = sjson.Set(choiceJSON, "index", idx)
		choiceJSON, _ = sjson.SetRaw(choiceJSON, "delta", delta)
		if finishReason != "" {
			choiceJSON, _ = sjson.Set(choiceJSON, "finish_reason", finishReason)
		}

		chunk, _ = sjson.SetRaw(chunk, fmt.Sprintf("choices.%d", i), choiceJSON)
	}

	if usage := root.Get("usage").Raw; usage != "" {
		chunk, _ = sjson.SetRaw(chunk, "usage", usage)
	}

	var buf strings.Builder
	buf.WriteString("data: ")
	buf.WriteString(chunk)
	buf.WriteString("\n\n")
	buf.WriteString("data: [DONE]\n\n")
	return []byte(buf.String())
}

// BuildClaudeFinalOnlySSE builds a final-only SSE response for Claude /v1/messages
// from a non-streaming response body. Returns SSE formatted bytes.
func BuildClaudeFinalOnlySSE(nonStreamResp []byte) []byte {
	root := gjson.ParseBytes(nonStreamResp)

	var buf strings.Builder

	messageData := `{"type":"message_start","message":` + string(nonStreamResp) + `}`
	buf.WriteString("event: message_start\ndata: ")
	buf.WriteString(messageData)
	buf.WriteString("\n\n")

	contentBlocks := root.Get("content").Array()
	for i, block := range contentBlocks {
		blockType := block.Get("type").String()

		startData, _ := json.Marshal(map[string]any{
			"type":          "content_block_start",
			"index":         i,
			"content_block": json.RawMessage(block.Raw),
		})
		buf.WriteString("event: content_block_start\ndata: ")
		buf.Write(startData)
		buf.WriteString("\n\n")

		if blockType == "text" || blockType == "thinking" {
			var deltaData []byte
			if blockType == "text" {
				text := block.Get("text").String()
				deltaData, _ = json.Marshal(map[string]any{
					"type":  "content_block_delta",
					"index": i,
					"delta": map[string]any{
						"type": "text_delta",
						"text": text,
					},
				})
			} else {
				thinking := block.Get("thinking").String()
				deltaData, _ = json.Marshal(map[string]any{
					"type":  "content_block_delta",
					"index": i,
					"delta": map[string]any{
						"type":     "thinking_delta",
						"thinking": thinking,
					},
				})
			}
			buf.WriteString("event: content_block_delta\ndata: ")
			buf.Write(deltaData)
			buf.WriteString("\n\n")
		}

		stopData, _ := json.Marshal(map[string]any{
			"type":  "content_block_stop",
			"index": i,
		})
		buf.WriteString("event: content_block_stop\ndata: ")
		buf.Write(stopData)
		buf.WriteString("\n\n")
	}

	buf.WriteString("event: message_delta\ndata: ")
	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   root.Get("stop_reason").String(),
			"stop_sequence": nil,
		},
	}
	if usage := root.Get("usage").Value(); usage != nil {
		messageDelta["usage"] = usage
	}
	deltaBytes, _ := json.Marshal(messageDelta)
	buf.Write(deltaBytes)
	buf.WriteString("\n\n")

	buf.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")

	return []byte(buf.String())
}

// BuildGeminiFinalOnlySSE builds a final-only response for Gemini streamGenerateContent.
// If alt is empty (SSE mode), wraps the response as "data: <json>\n\n".
// Otherwise, returns the raw JSON.
func BuildGeminiFinalOnlySSE(nonStreamResp []byte, alt string) []byte {
	if alt == "" {
		var buf strings.Builder
		buf.WriteString("data: ")
		buf.Write(nonStreamResp)
		buf.WriteString("\n\n")
		return []byte(buf.String())
	}
	return nonStreamResp
}
