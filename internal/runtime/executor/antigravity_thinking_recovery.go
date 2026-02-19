package executor

import (
	"bytes"
	"context"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

// executeClaudeNonStreamWithRecovery wraps executeClaudeNonStream with thinking error recovery.
// This is a fork-specific enhancement that retries on thinking signature errors.
func (e *AntigravityExecutor) executeClaudeNonStreamWithRecovery(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	originalPayload := bytes.Clone(req.Payload)
	if len(opts.OriginalRequest) > 0 {
		originalPayload = bytes.Clone(opts.OriginalRequest)
	}

	sessionID := deriveSessionIDFromClaudeMessages(originalPayload)

	// Pre-sanitize payload to remove known invalid signatures
	if sanitized, changed := sanitizeAntigravityPayloadForInvalidThinking(originalPayload, sessionID); changed {
		log.Debugf("Antigravity: Pre-sanitized payload for session %s", sessionID)
		req.Payload = sanitized
		opts.OriginalRequest = sanitized
	}

	// First attempt
	resp, err := e.executeClaudeNonStream(ctx, auth, req, opts)
	if err == nil {
		return resp, nil
	}

	// Check if this is a thinking signature error
	sErr, ok := err.(statusErr)
	if !ok {
		return resp, err
	}

	if newPayload, shouldRetry := handleAntigravityThinkingErrorRecovery(sessionID, originalPayload, []byte(sErr.msg), sErr.code); shouldRetry {
		log.Infof("Antigravity: Detected thinking signature error. Attempting recovery. sessionID=%s", sessionID)
		req.Payload = newPayload
		opts.OriginalRequest = newPayload
		return e.executeClaudeNonStream(ctx, auth, req, opts)
	}

	return resp, err
}

// executeClaudeStreamWithRecovery wraps executeStreamInternal with thinking error recovery for Claude models.
// This is a fork-specific enhancement that retries on thinking signature errors.
func (e *AntigravityExecutor) executeClaudeStreamWithRecovery(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	originalPayload := bytes.Clone(req.Payload)
	if len(opts.OriginalRequest) > 0 {
		originalPayload = bytes.Clone(opts.OriginalRequest)
	}

	sessionID := deriveSessionIDFromClaudeMessages(originalPayload)

	// Pre-sanitize payload to remove known invalid signatures
	if sanitized, changed := sanitizeAntigravityPayloadForInvalidThinking(originalPayload, sessionID); changed {
		log.Debugf("Antigravity Stream: Pre-sanitized payload for session %s", sessionID)
		req.Payload = sanitized
		opts.OriginalRequest = sanitized
	}

	// First attempt - use executeStreamInternal which is the actual implementation
	stream, err := e.executeStreamInternal(ctx, auth, req, opts)
	if err == nil {
		return stream, nil
	}

	// Check if this is a thinking signature error
	sErr, ok := err.(statusErr)
	if !ok {
		return stream, err
	}

	if newPayload, shouldRetry := handleAntigravityThinkingErrorRecovery(sessionID, originalPayload, []byte(sErr.msg), sErr.code); shouldRetry {
		log.Infof("Antigravity Stream: Detected thinking signature error. Attempting recovery. sessionID=%s", sessionID)
		req.Payload = newPayload
		opts.OriginalRequest = newPayload
		return e.executeStreamInternal(ctx, auth, req, opts)
	}

	return stream, err
}
