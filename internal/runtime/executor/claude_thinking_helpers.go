package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var anthropicErrorPosRegex = regexp.MustCompile(`messages\.(\d+)\.content\.(\d+)`)

// deriveSessionIDFromClaudeMessages generates a stable session ID from the request.
// Uses the hash of the first user message to identify the conversation.
func deriveSessionIDFromClaudeMessages(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	for _, msg := range messages.Array() {
		if msg.Get("role").String() == "user" {
			content := msg.Get("content").String()
			if content == "" {
				// Try to get text from content array
				content = msg.Get("content.0.text").String()
			}
			if content != "" {
				h := sha256.Sum256([]byte(content))
				return hex.EncodeToString(h[:16])
			}
		}
	}
	return ""
}

// isClaudeThinkingInvalidSignatureError returns true if the error is a thinking signature mismatch.
func isClaudeThinkingInvalidSignatureError(statusCode int, body []byte) bool {
	if statusCode != 400 {
		return false
	}
	// Try JSON first
	msg := gjson.GetBytes(body, "error.message").String()
	if msg == "" {
		msg = gjson.GetBytes(body, "message").String()
	}
	if msg == "" {
		msg = string(body)
	}

	// Case-insensitive check for common patterns
	msgLower := strings.ToLower(msg)
	return strings.Contains(msgLower, "invalid signature") && strings.Contains(msgLower, "thinking")
}

// extractInvalidThinkingPath parses positional errors like messages.1.content.0
func extractInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool) {
	msg := gjson.GetBytes(errBody, "error.message").String()
	if msg == "" {
		msg = gjson.GetBytes(errBody, "message").String()
	}
	if msg == "" {
		msg = string(errBody)
	}

	m := anthropicErrorPosRegex.FindStringSubmatch(msg)
	if len(m) != 3 {
		return 0, 0, false
	}
	mi, _ := strconv.Atoi(m[1])
	pi, _ := strconv.Atoi(m[2])
	return mi, pi, true
}

// recordInvalidThinkingSignatureFromError extracts the signature from the sent payload and caches it.
func recordInvalidThinkingSignatureFromError(sessionID string, sentPayload []byte, errBody []byte) (string, bool) {
	if sessionID == "" {
		return "", false
	}

	msgIndex, partIndex, ok := extractInvalidThinkingPath(errBody)
	if !ok {
		return "", false
	}

	path := fmt.Sprintf("messages.%d.content.%d.signature", msgIndex, partIndex)
	sig := gjson.GetBytes(sentPayload, path).String()
	if sig == "" {
		return "", false
	}

	cache.CacheInvalidSignature(sessionID, sig)
	return sig, true
}

// sanitizeClaudePayloadForInvalidThinking pre-emptively removes blacklisted signatures.
func sanitizeClaudePayloadForInvalidThinking(payload []byte, sessionID string) ([]byte, bool) {
	if sessionID == "" {
		return payload, false
	}

	changed := false
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return payload, false
	}

	newPayload := payload
	messages.ForEach(func(mIdx, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}

		content.ForEach(func(cIdx, part gjson.Result) bool {
			if part.Get("type").String() == "thinking" {
				sig := part.Get("signature").String()
				if sig != "" && cache.IsInvalidSignature(sessionID, sig) {
					// Replace thinking block with text placeholder
					path := fmt.Sprintf("messages.%d.content.%d", mIdx.Int(), cIdx.Int())
					placeholder := map[string]string{
						"type": "text",
						"text": "[Previous thinking omitted due to invalid signature]",
					}
					pb, _ := json.Marshal(placeholder)
					newPayload, _ = sjson.SetRawBytes(newPayload, path, pb)
					changed = true
				}
			}
			return true
		})
		return true
	})

	return newPayload, changed
}

// stripClaudeThinkingBlocksForRetry is a fallback to remove ALL thinking blocks if specific path is unknown.
func stripClaudeThinkingBlocksForRetry(payload []byte) ([]byte, bool) {
	changed := false
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return payload, false
	}

	newPayload := payload
	messages.ForEach(func(mIdx, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}

		content.ForEach(func(cIdx, part gjson.Result) bool {
			if part.Get("type").String() == "thinking" {
				path := fmt.Sprintf("messages.%d.content.%d", mIdx.Int(), cIdx.Int())
				placeholder := map[string]string{
					"type": "text",
					"text": "[Previous thinking omitted due to invalid signature]",
				}
				pb, _ := json.Marshal(placeholder)
				newPayload, _ = sjson.SetRawBytes(newPayload, path, pb)
				changed = true
			}
			return true
		})
		return true
	})

	return newPayload, changed
}
