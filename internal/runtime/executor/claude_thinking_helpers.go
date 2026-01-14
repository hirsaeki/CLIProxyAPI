package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var anthropicErrorPosRegex = regexp.MustCompile(`messages\.(\d+)\.content\.(\d+)`)

// thinkingPlaceholder is used when invalid thinking blocks are stripped
const thinkingPlaceholder = `{"type":"text","text":"[Previous thinking omitted due to invalid signature]"}`

// extractErrorMessage extracts the error message from various response formats
func extractErrorMessage(body []byte) string {
	if msg := gjson.GetBytes(body, "error.message").String(); msg != "" {
		return msg
	}
	if msg := gjson.GetBytes(body, "message").String(); msg != "" {
		return msg
	}
	return string(body)
}

// deriveSessionIDFromClaudeMessages generates a stable session ID from the request.
// Uses the hash of the first user message to identify the conversation.
func deriveSessionIDFromClaudeMessages(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	for _, msg := range messages.Array() {
		if msg.Get("role").String() != "user" {
			continue
		}
		content := msg.Get("content").String()
		if content == "" {
			content = msg.Get("content.0.text").String()
		}
		if content != "" {
			h := sha256.Sum256([]byte(content))
			return hex.EncodeToString(h[:16])
		}
	}
	return ""
}

// isClaudeThinkingInvalidSignatureError returns true if the error is a thinking signature mismatch.
func isClaudeThinkingInvalidSignatureError(statusCode int, body []byte) bool {
	if statusCode != 400 {
		return false
	}
	msgLower := strings.ToLower(extractErrorMessage(body))
	return strings.Contains(msgLower, "invalid signature") && strings.Contains(msgLower, "thinking")
}

// extractInvalidThinkingPath parses positional errors like messages.1.content.0
func extractInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool) {
	m := anthropicErrorPosRegex.FindStringSubmatch(extractErrorMessage(errBody))
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

// replaceThinkingBlocks iterates through messages and replaces thinking blocks based on the predicate.
// If predicate is nil, all thinking blocks are replaced.
func replaceThinkingBlocks(payload []byte, shouldReplace func(signature string) bool) ([]byte, bool) {
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return payload, false
	}

	changed := false
	messages.ForEach(func(mIdx, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(cIdx, part gjson.Result) bool {
			if part.Get("type").String() != "thinking" {
				return true
			}
			if shouldReplace != nil && !shouldReplace(part.Get("signature").String()) {
				return true
			}
			path := fmt.Sprintf("messages.%d.content.%d", mIdx.Int(), cIdx.Int())
			payload, _ = sjson.SetRawBytes(payload, path, []byte(thinkingPlaceholder))
			changed = true
			return true
		})
		return true
	})
	return payload, changed
}

// sanitizeClaudePayloadForInvalidThinking pre-emptively removes blacklisted signatures.
func sanitizeClaudePayloadForInvalidThinking(payload []byte, sessionID string) ([]byte, bool) {
	if sessionID == "" {
		return payload, false
	}
	return replaceThinkingBlocks(payload, func(sig string) bool {
		return sig != "" && cache.IsInvalidSignature(sessionID, sig)
	})
}

// stripClaudeThinkingBlocksForRetry is a fallback to remove ALL thinking blocks if specific path is unknown.
func stripClaudeThinkingBlocksForRetry(payload []byte) ([]byte, bool) {
	return replaceThinkingBlocks(payload, nil)
}

// handleThinkingErrorRecovery processes a thinking signature error and returns the sanitized payload for retry.
// Returns the new payload and true if recovery should be attempted, or original payload and false otherwise.
func handleThinkingErrorRecovery(sessionID string, payload, errBody []byte, statusCode int) ([]byte, bool) {
	if !isClaudeThinkingInvalidSignatureError(statusCode, errBody) {
		return payload, false
	}
	if sig, ok := recordInvalidThinkingSignatureFromError(sessionID, payload, errBody); ok {
		log.Debugf("Recorded invalid signature: %s", sig)
		newPayload, _ := sanitizeClaudePayloadForInvalidThinking(payload, sessionID)
		return newPayload, true
	}
	log.Debugf("Failed to record specific signature, falling back to full strip")
	newPayload, _ := stripClaudeThinkingBlocksForRetry(payload)
	return newPayload, true
}
