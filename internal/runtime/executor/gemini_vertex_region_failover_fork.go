package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const (
	vertexRegionPolicyKeyFork    = "vertex_region_failover"
	vertexRegionCapacityMaxFork  = 32 << 10
	vertexRegionMinCooldownFork  = time.Second
	vertexRegionMaxRetriesFork   = 4
	vertexRegionMaxLocationsFork = 16
	vertexRegionMaxWaitFork      = 8 * time.Second
)

var vertexRegionRouterFork = vertexRegionRouteFork{
	cooldown: &helps.VertexRegionCooldownFork{},
	now:      time.Now,
	wait:     helps.VertexRegionWaitFork,
	delay:    helps.VertexRegionBackoffFork,
}

type vertexRegionRouteFork struct {
	cooldown *helps.VertexRegionCooldownFork
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
	delay    func(int) time.Duration
}

// This policy only accepts canonical endpoint locations and explicit model
// support declarations. Eligibility is supplied by the operator and checked
// against the Google locations reference, not inferred from a catalog alias.
func vertexRegionLocationsFork(auth *cliproxyauth.Auth, model, primary string) []string {
	if auth == nil || !vertexRegionTextModelFork(model) {
		return nil
	}
	models, ok := auth.Metadata[vertexRegionPolicyKeyFork].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := models[model].([]any)
	if !ok || len(raw) < 2 || len(raw) > vertexRegionMaxLocationsFork {
		return nil
	}
	locations := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		location, okString := item.(string)
		if !okString || !vertexRegionLocationCodeFork(location) || seen[location] {
			return nil
		}
		seen[location] = true
		locations = append(locations, location)
	}
	if locations[0] != primary {
		return nil
	}
	return locations
}

// Restrict destinations to Google-documented Gemini endpoint locations. The
// location set is not permission to route; the exact model list must opt in.
// Model availability within this set is validated by the operator.
func vertexRegionLocationCodeFork(location string) bool {
	switch location {
	case "global", "us", "eu",
		"us-west1", "us-west4", "us-central1", "us-east1", "us-east4", "us-east5", "us-south1",
		"northamerica-northeast1", "southamerica-east1",
		"europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-west6", "europe-west8", "europe-west9",
		"europe-north1", "europe-central2", "europe-southwest1",
		"asia-south1", "asia-southeast1", "asia-east1", "asia-east2", "asia-northeast1", "asia-northeast3",
		"australia-southeast1", "me-west1", "me-central1", "me-central2":
		return true
	default:
		return false
	}
}

func vertexRegionTextModelFork(model string) bool {
	if !strings.HasPrefix(model, "gemini-") || strings.Contains(model, "/") {
		return false
	}
	for _, class := range []string{"-image", "-tts", "-embedding", "-live", "-omni", "-audio", "-robot"} {
		if strings.Contains(model, class) {
			return false
		}
	}
	return true
}

// Keep stateful APIs and less predictable translation routes out of the
// first rollout even if their final payload looks like plain text.
func vertexRegionProtocolFork(opts cliproxyexecutor.Options) bool {
	for _, format := range []sdktranslator.Format{opts.SourceFormat, cliproxyexecutor.ResponseFormatOrSource(opts)} {
		if format != sdktranslator.FormatGemini && format != sdktranslator.FormatOpenAI {
			return false
		}
	}
	return true
}

func vertexRegionPlainTextFork(body []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil || len(payload) == 0 {
		return false
	}
	validContent := func(value any) bool {
		content, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for key := range content {
			if key != "role" && key != "parts" {
				return false
			}
		}
		parts, ok := content["parts"].([]any)
		if !ok || len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			item, okPart := part.(map[string]any)
			text, okText := item["text"].(string)
			if !okPart || !okText || len(item) != 1 || text == "" {
				return false
			}
		}
		return true
	}
	for key, value := range payload {
		switch key {
		case "contents":
			items, ok := value.([]any)
			if !ok || len(items) == 0 {
				return false
			}
			for _, item := range items {
				if !validContent(item) {
					return false
				}
			}
		case "systemInstruction":
			if !validContent(value) {
				return false
			}
		case "generationConfig":
			cfg, ok := value.(map[string]any)
			if !ok {
				return false
			}
			// Do not allow future generationConfig fields to silently opt in.
			for setting := range cfg {
				switch setting {
				case "temperature", "topP", "topK", "candidateCount", "maxOutputTokens",
					"stopSequences", "presencePenalty", "frequencyPenalty", "seed",
					"responseMimeType", "responseSchema", "responseJsonSchema", "responseLogprobs",
					"logprobs", "thinkingConfig":
				case "responseModalities":
					items, okItems := cfg[setting].([]any)
					if !okItems || len(items) != 1 || items[0] != "TEXT" {
						return false
					}
				default:
					return false
				}
			}
		case "model", "safetySettings":
		default:
			return false
		}
	}
	_, hasContents := payload["contents"]
	return hasContents
}

func vertexRegionCapacity429Fork(status int, body []byte) bool {
	if status != http.StatusTooManyRequests || len(body) > vertexRegionCapacityMaxFork ||
		!ojson.ValidBytes(body) ||
		ojson.GetBytesNoCopy(body, "error.code").Int() != http.StatusTooManyRequests ||
		ojson.GetBytesNoCopy(body, "error.status").String() != "RESOURCE_EXHAUSTED" {
		return false
	}
	msg := strings.ToLower(ojson.GetBytesNoCopy(body, "error.message").String())
	// Generic RESOURCE_EXHAUSTED and the documented PAYG retry-later message
	// cannot distinguish project quota or acceleration limits from capacity.
	for _, deny := range []string{"quota", "provisioned", "throughput", "rate limit", "acceleration", "billing", "permission", "authorization", "limit", "project"} {
		if strings.Contains(msg, deny) {
			return false
		}
	}
	if !(strings.Contains(msg, "regional capacity") || strings.Contains(msg, "capacity in this region") || strings.Contains(msg, "capacity in this location")) ||
		!(strings.Contains(msg, "temporar") || strings.Contains(msg, "overload") || strings.Contains(msg, "try again later")) {
		return false
	}
	if details := ojson.GetBytesNoCopy(body, "error.details"); details.Exists() {
		if !details.IsArray() {
			return false
		}
		for _, d := range details.Array() {
			// QuotaFailure, ErrorInfo and unknown kinds fail closed.
			if d.Get("@type").String() != "type.googleapis.com/google.rpc.RetryInfo" {
				return false
			}
		}
	}
	return true
}

// Regional capacity stays local to the route cooldown. It is not credential
// or model quota exhaustion in the Manager's generic cooldown ledger.
type vertexRegionCapacityFailureFork struct{ error }

func (e vertexRegionCapacityFailureFork) Unwrap() error         { return e.error }
func (e vertexRegionCapacityFailureFork) IsRequestScoped() bool { return true }

type vertexRegionStopFork struct{ error }

func (e vertexRegionStopFork) Unwrap() error       { return e.error }
func (e vertexRegionStopFork) IsRequestStop() bool { return true }

func vertexRegionStopErrorFork(err error) error {
	if err == nil {
		return err
	}
	return vertexRegionStopFork{err}
}

type vertexRegionPrefixedBodyFork struct {
	io.Reader
	io.Closer
}

func (e *GeminiVertexExecutor) doVertexRegionRequestFork(client *http.Client, req *http.Request, auth *cliproxyauth.Auth, opts cliproxyexecutor.Options, project, model, primary, action string, info helps.UpstreamRequestLog) (*http.Response, bool, error) {
	record := func(attempt *http.Request) {
		info.URL = attempt.URL.String()
		helps.RecordAPIRequest(req.Context(), e.cfg, info)
	}
	locations := vertexRegionLocationsFork(auth, model, primary)
	if len(locations) == 0 || !vertexRegionProtocolFork(opts) || !vertexRegionRequestEligibleFork(req, project, model, primary, action) || !vertexRegionPlainTextFork(info.Body) {
		record(req)
		resp, err := client.Do(req)
		return resp, false, err
	}
	defer func() { _ = req.Body.Close() }()
	retries, maxWait := 0, time.Duration(0)
	if e.cfg != nil && e.cfg.RequestRetry > 0 && e.cfg.MaxRetryInterval > 0 {
		retries = min(e.cfg.RequestRetry, vertexRegionMaxRetriesFork)
		if override, ok := auth.RequestRetryOverride(); ok {
			retries = min(retries, override)
		}
		maxWait = time.Duration(min(e.cfg.MaxRetryInterval, int(vertexRegionMaxWaitFork/time.Second))) * time.Second
	}
	recordCapacity := func(resp *http.Response, raw []byte) {
		helps.RecordAPIResponseMetadata(req.Context(), e.cfg, resp.StatusCode, resp.Header.Clone())
		helps.AppendAPIResponseChunk(req.Context(), e.cfg, raw)
	}
	resp, err := vertexRegionRouterFork.do(client, req, project, model, action, locations, retries, maxWait, record, recordCapacity)
	return resp, true, err
}

func vertexRegionRequestEligibleFork(req *http.Request, project, model, primary, action string) bool {
	if req.Method != http.MethodPost || req.GetBody == nil || req.Body == nil || req.URL.Scheme != "https" ||
		req.URL.User != nil || req.Host != req.URL.Host || req.URL.RawPath != "" || req.URL.Fragment != "" || req.URL.ForceQuery {
		return false
	}
	switch action {
	case "generateContent":
		if req.URL.RawQuery != "" {
			return false
		}
	case "streamGenerateContent":
		if req.URL.RawQuery != "alt=sse" {
			return false
		}
	default:
		return false
	}
	for _, id := range []string{project, model} {
		if id == "" || strings.IndexFunc(id, func(c rune) bool {
			return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.')
		}) >= 0 {
			return false
		}
	}
	for key := range req.Header {
		switch strings.ToLower(key) {
		case "x-vertex-ai-llm-request-type", "x-goog-request-params", "x-goog-user-project", "x-goog-api-key", "host":
			return false
		}
	}
	primaryURL := vertexBaseURL(primary) + vertexRegionPathFork(project, model, primary, action)
	return req.URL.Scheme+"://"+req.URL.Host+req.URL.Path == primaryURL
}

func vertexRegionPathFork(project, model, location, action string) string {
	return fmt.Sprintf("/%s/projects/%s/locations/%s/publishers/google/models/%s:%s", vertexAPIVersion, project, location, model, action)
}

func (p vertexRegionRouteFork) do(original *http.Client, req *http.Request, project, model, action string, locations []string, retries int, maxWait time.Duration, record func(*http.Request), recordCapacity func(*http.Response, []byte)) (*http.Response, error) {
	ctx := req.Context()
	// A route never forwards credentials to a redirect target, even within
	// Google. Only the explicitly approved, generated URL below is sent.
	client := *original
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	start := p.now()
	waitLimit := start.Add(maxWait)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(waitLimit) {
		waitLimit = deadline
	}
	var lastErr statusErr
	capacitySeen := false
	attempt := func(location string) (*http.Response, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		key := helps.VertexRegionKeyFork{Project: project, Model: model, Location: location}
		finish, until := p.cooldown.Claim(key, p.now())
		if finish == nil {
			if !until.IsZero() {
				capacitySeen = true
			}
			return nil, false, nil
		}
		defer func() {
			if finish != nil {
				finish(time.Time{})
			}
		}()
		target, err := url.Parse(vertexBaseURL(location))
		if err != nil {
			return nil, false, err
		}
		attemptReq := req.Clone(ctx)
		attemptReq.URL.Scheme, attemptReq.URL.Host = target.Scheme, target.Host
		attemptReq.URL.Path = vertexRegionPathFork(project, model, location, action)
		attemptReq.Host = target.Host
		attemptReq.Body, err = req.GetBody()
		if err != nil {
			return nil, false, err
		}
		record(attemptReq)
		resp, err := client.Do(attemptReq)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, true, err
		}
		prefix, errRead := io.ReadAll(io.LimitReader(resp.Body, vertexRegionCapacityMaxFork+1))
		if errRead != nil || !vertexRegionCapacity429Fork(resp.StatusCode, prefix) {
			// Preserve the original response for native error handling.
			resp.Body = vertexRegionPrefixedBodyFork{io.MultiReader(bytes.NewReader(prefix), resp.Body), resp.Body}
			return resp, true, nil
		}
		_ = resp.Body.Close()
		capacitySeen = true
		now := p.now()
		delay := vertexRegionMinCooldownFork
		if header := helps.ParseGoogleRetryAfter(resp.StatusCode, resp.Header, nil, now); header != nil {
			delay = max(delay, *header)
		}
		if body := helps.ParseGoogleRetryAfter(resp.StatusCode, nil, prefix, now); body != nil {
			delay = max(delay, *body)
		}
		finish(now.Add(delay))
		finish = nil
		recordCapacity(resp, prefix)
		lastErr = newGeminiStatusErr(resp.StatusCode, resp.Header, prefix)
		helps.LogWithRequestID(ctx).WithField("location", location).Debug("vertex regional capacity cooldown")
		return nil, true, nil
	}

	earliestReady := func(now time.Time) time.Time {
		var earliest time.Time
		for _, location := range locations {
			due := p.cooldown.Ready(helps.VertexRegionKeyFork{Project: project, Model: model, Location: location}, now)
			if earliest.IsZero() || due.Before(earliest) {
				earliest = due
			}
		}
		return earliest
	}

	for _, location := range locations {
		if resp, _, err := attempt(location); resp != nil || err != nil {
			return resp, err
		}
	}
	// Each Phase 2 iteration can issue at most one new HTTP request. Skip a
	// cooldown atomically again after waiting because another request may
	// have refreshed it. The fixed loop bound also limits contention waits.
	for retry := 0; capacitySeen && retry < retries; retry++ {
		now := p.now()
		ready := now.Add(p.delay(retry))
		if earliest := earliestReady(now); earliest.After(ready) {
			ready = earliest
		}
		if !ready.Before(waitLimit) {
			break
		}
		if err := p.wait(ctx, ready.Sub(now)); err != nil {
			return nil, err
		}
		if !p.now().Before(waitLimit) {
			break
		}
		for _, location := range locations {
			resp, sent, err := attempt(location)
			if resp != nil || err != nil {
				return resp, err
			}
			if sent {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lastErr.code == 0 {
		lastErr = statusErr{code: http.StatusTooManyRequests, msg: "vertex approved endpoints are cooling down"}
	}
	// The public hint is when any approved endpoint can be tried again, not
	// the last response's possibly longer cooldown on a specific endpoint.
	lastErr.retryAfter = nil
	now := p.now()
	if delay := earliestReady(now).Sub(now); delay > 0 {
		lastErr.retryAfter = &delay
	}
	return nil, vertexRegionCapacityFailureFork{lastErr}
}

// The HTTP router ends at 2xx. Do not let bootstrap retry or re-select the
// credential/model if a 2xx stream later fails before its first payload.
func vertexRegionStreamFork(ctx context.Context, upstream <-chan cliproxyexecutor.StreamChunk) <-chan cliproxyexecutor.StreamChunk {
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		saw := false
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-upstream:
				if !ok {
					if !saw {
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: vertexRegionStopErrorFork(statusErr{code: http.StatusBadGateway, msg: "vertex stream closed before first payload"})}:
						case <-ctx.Done():
						}
					}
					return
				}
				if chunk.Err != nil {
					chunk.Err = vertexRegionStopErrorFork(chunk.Err)
				}
				saw = saw || len(chunk.Payload) > 0 || chunk.Err != nil
				select {
				case out <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
