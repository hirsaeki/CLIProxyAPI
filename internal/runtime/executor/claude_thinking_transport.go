package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"
)

// claudeThinkingRecoveryTransport wraps an http.RoundTripper to transparently
// handle Claude thinking signature errors by retrying once with a sanitized payload.
// This isolates retry logic from claude_executor.go to minimize upstream merge conflicts.
type claudeThinkingRecoveryTransport struct {
	base           http.RoundTripper
	sessionID      string
	initialPayload []byte
	ctx            context.Context
}

func (t *claudeThinkingRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.isClaudeMessagesRequest(req) {
		return t.base.RoundTrip(req)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	if resp.StatusCode != http.StatusBadRequest {
		return resp, nil
	}

	b, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return resp, nil
	}

	newPayload, shouldRetry := handleThinkingErrorRecovery(t.sessionID, t.initialPayload, b, resp.StatusCode)
	if !shouldRetry {
		resp.Body = io.NopCloser(bytes.NewReader(b))
		return resp, nil
	}

	if t.ctx != nil {
		logWithRequestID(t.ctx).Infof("Detected Claude thinking signature error. Attempting recovery. sessionID=%s", t.sessionID)
	} else {
		log.Infof("Detected Claude thinking signature error. Attempting recovery. sessionID=%s", t.sessionID)
	}

	retryReq := req.Clone(req.Context())
	retryReq.Body = io.NopCloser(bytes.NewReader(newPayload))
	retryReq.ContentLength = int64(len(newPayload))
	retryReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(newPayload)), nil
	}

	return t.base.RoundTrip(retryReq)
}

func (t *claudeThinkingRecoveryTransport) isClaudeMessagesRequest(req *http.Request) bool {
	if req == nil || req.URL == nil {
		return false
	}
	return strings.Contains(req.URL.Path, "/v1/messages")
}

// claudeThinkingPreRequest computes sessionID and sanitizes payload using cached invalid signatures.
// Returns the sessionID for later use and the sanitized payload.
func claudeThinkingPreRequest(bodyForUpstream, bodyForTranslation []byte) (sessionID string, sanitizedPayload []byte) {
	sessionID = deriveSessionIDFromClaudeMessages(bodyForTranslation)
	if sessionID == "" {
		return "", bodyForUpstream
	}
	sanitized, _ := sanitizeClaudePayloadForInvalidThinking(bodyForUpstream, sessionID)
	if len(sanitized) == 0 {
		return sessionID, bodyForUpstream
	}
	return sessionID, sanitized
}

// wrapHTTPClientWithThinkingRecovery wraps the HTTP client with thinking error recovery transport.
// The ctx parameter is used for request ID logging.
func wrapHTTPClientWithThinkingRecovery(client *http.Client, sessionID string, initialPayload []byte, ctx context.Context) *http.Client {
	if client == nil || sessionID == "" {
		return client
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped := *client
	wrapped.Transport = &claudeThinkingRecoveryTransport{
		base:           base,
		sessionID:      sessionID,
		initialPayload: append([]byte(nil), initialPayload...),
		ctx:            ctx,
	}
	return &wrapped
}
