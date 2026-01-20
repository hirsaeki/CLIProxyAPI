package executor

import (
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// isAntigravityThinkingSignatureError returns true if the error is a thinking signature mismatch.
// Antigravity wraps Anthropic errors in Google RPC envelope:
//
//	{"error":{"code":400,"message":"{\"type\":\"error\",\"error\":{...}}","status":"INVALID_ARGUMENT"}}
func isAntigravityThinkingSignatureError(statusCode int, body []byte) bool {
	if statusCode != 400 {
		return false
	}

	// Extract the inner Anthropic error message from Google RPC envelope
	msgLower := strings.ToLower(extractAntigravityErrorMessage(body))
	return strings.Contains(msgLower, "invalid") &&
		strings.Contains(msgLower, "signature") &&
		strings.Contains(msgLower, "thinking")
}

// extractAntigravityErrorMessage extracts the error message from Antigravity response.
// It handles the nested JSON structure where Anthropic error is embedded as a string.
func extractAntigravityErrorMessage(body []byte) string {
	// Try Google RPC error.message first (may contain embedded JSON)
	outerMsg := gjson.GetBytes(body, "error.message").String()
	if outerMsg != "" {
		// Check if it's embedded JSON (Anthropic error wrapped in string)
		if strings.HasPrefix(outerMsg, "{") {
			innerMsg := gjson.Get(outerMsg, "error.message").String()
			if innerMsg != "" {
				return innerMsg
			}
		}
		return outerMsg
	}

	// Fallback: direct message field
	if msg := gjson.GetBytes(body, "message").String(); msg != "" {
		return msg
	}

	return string(body)
}

// extractAntigravityInvalidThinkingPath parses positional errors like "messages.1.content.0"
// from Antigravity error responses.
func extractAntigravityInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool) {
	msg := extractAntigravityErrorMessage(errBody)
	m := anthropicErrorPosRegex.FindStringSubmatch(msg)
	if len(m) != 3 {
		return 0, 0, false
	}
	mi, _ := parseInt(m[1])
	pi, _ := parseInt(m[2])
	return mi, pi, true
}

// parseInt is a helper to parse string to int
func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// recordAntigravityInvalidSignature extracts the signature from the original Claude payload
// and caches it as invalid for future requests.
func recordAntigravityInvalidSignature(sessionID string, originalPayload []byte, errBody []byte) (string, bool) {
	if sessionID == "" {
		return "", false
	}

	msgIndex, partIndex, ok := extractAntigravityInvalidThinkingPath(errBody)
	if !ok {
		return "", false
	}

	// Extract signature from original Claude format payload
	path := fmt.Sprintf("messages.%d.content.%d.signature", msgIndex, partIndex)
	sig := gjson.GetBytes(originalPayload, path).String()
	if sig == "" {
		return "", false
	}

	cache.CacheInvalidSignature(sessionID, sig)
	return sig, true
}

// replaceAntigravityThinkingBlocks iterates through messages and replaces thinking blocks
// based on the predicate. If predicate is nil, all thinking blocks are replaced.
// This operates on Claude format payload (not Antigravity format).
func replaceAntigravityThinkingBlocks(payload []byte, shouldReplace func(signature string) bool) ([]byte, bool) {
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

// sanitizeAntigravityPayloadForInvalidThinking pre-emptively removes blacklisted signatures.
// This operates on Claude format payload.
func sanitizeAntigravityPayloadForInvalidThinking(payload []byte, sessionID string) ([]byte, bool) {
	if sessionID == "" {
		return payload, false
	}
	return replaceAntigravityThinkingBlocks(payload, func(sig string) bool {
		return sig != "" && cache.IsInvalidSignature(sessionID, sig)
	})
}

// stripAntigravityThinkingBlocksForRetry is a fallback to remove ALL thinking blocks
// if specific path is unknown. This operates on Claude format payload.
func stripAntigravityThinkingBlocksForRetry(payload []byte) ([]byte, bool) {
	return replaceAntigravityThinkingBlocks(payload, nil)
}

// handleAntigravityThinkingErrorRecovery processes a thinking signature error
// and returns the sanitized payload for retry.
// Returns the new payload and true if recovery should be attempted, or original payload and false otherwise.
func handleAntigravityThinkingErrorRecovery(sessionID string, originalPayload, errBody []byte, statusCode int) ([]byte, bool) {
	if !isAntigravityThinkingSignatureError(statusCode, errBody) {
		return originalPayload, false
	}

	if sig, ok := recordAntigravityInvalidSignature(sessionID, originalPayload, errBody); ok {
		log.Debugf("Antigravity: Recorded invalid signature: %s", sig)
		newPayload, _ := sanitizeAntigravityPayloadForInvalidThinking(originalPayload, sessionID)
		return newPayload, true
	}

	log.Debugf("Antigravity: Failed to record specific signature, falling back to full strip")
	newPayload, _ := stripAntigravityThinkingBlocksForRetry(originalPayload)
	return newPayload, true
}
