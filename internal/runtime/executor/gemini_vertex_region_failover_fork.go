package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const (
	vertexRegionPolicyKeyFork    = vertexlocations.PolicyKeyFork
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
	automatic        bool
	cooldown         *helps.VertexRegionCooldownFork
	now              func() time.Time
	wait             func(context.Context, time.Duration) error
	delay            func(int) time.Duration
	secondaryAllowed func(string, time.Time) bool
	trace            *vertexRegionTraceFork
}

// Permission from either mode is never evidence of current destination support.
func vertexRegionLocationsFork(auth *cliproxyauth.Auth, model, primary string) []string {
	if auth == nil || !vertexRegionTextModelFork(model) {
		return nil
	}
	mode, _ := vertexlocations.CredentialModeFork(auth.Metadata)
	switch mode {
	case vertexlocations.ModeOffFork:
		return nil
	case vertexlocations.ModeAutomaticFork:
		return vertexlocations.AutomaticCandidatesFork(primary)
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

func vertexRegionLocationCodeFork(location string) bool {
	return vertexlocations.KnownLocation(location)
}

func vertexRegionTextModelFork(model string) bool {
	return vertexlocations.TextModel(model)
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

// vertexRegionGeneric429Fork recognizes only the explicit generic retry-later
// envelope. It is not evidence of regional capacity or of fixed quota.
func vertexRegionGeneric429Fork(body []byte) bool {
	if len(body) > vertexRegionCapacityMaxFork || !ojson.ValidBytes(body) {
		return false
	}
	root := ojson.ParseBytes(body)
	if !vertexRegionKnownKeysFork(root, "error") {
		return false
	}
	failure := root.Get("error")
	if !vertexRegionKnownKeysFork(failure, "code", "status", "message", "details") ||
		failure.Get("code").Raw != "429" || failure.Get("status").String() != "RESOURCE_EXHAUSTED" {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(failure.Get("message").String()))
	message = strings.TrimSuffix(message, " please refer to https://cloud.google.com/vertex-ai/generative-ai/docs/error-code-429 for more details.")
	switch message {
	case "resource exhausted. please try again later.", "resource exhausted, please try again later.":
	default:
		return false
	}
	details := failure.Get("details")
	if !details.Exists() {
		return true
	}
	if !details.IsArray() {
		return false
	}
	items := details.Array()
	if len(items) == 0 {
		return true
	}
	if len(items) != 1 || !vertexRegionKnownKeysFork(items[0], "@type", "retryDelay") ||
		items[0].Get("@type").String() != "type.googleapis.com/google.rpc.RetryInfo" {
		return false
	}
	value := items[0].Get("retryDelay").String()
	seconds, ok := strings.CutSuffix(value, "s")
	if !ok || len(seconds) == 0 || seconds[0] < '0' || seconds[0] > '9' || strings.IndexFunc(seconds, func(c rune) bool {
		return c != '.' && (c < '0' || c > '9')
	}) >= 0 {
		return false
	}
	if _, fraction, dotted := strings.Cut(seconds, "."); dotted && (len(fraction) == 0 || len(fraction) > 9) {
		return false
	}
	delay, err := time.ParseDuration(value)
	return err == nil && delay >= 0
}

type vertexRegion429ClassFork uint8

const (
	vertexRegionOther429Fork vertexRegion429ClassFork = iota
	vertexRegionCapacityClassFork
	vertexRegionPayGoClassFork
)

func classifyVertexRegion429Fork(body []byte) vertexRegion429ClassFork {
	if vertexRegionCapacity429Fork(http.StatusTooManyRequests, body) {
		return vertexRegionCapacityClassFork
	}
	if vertexRegionGeneric429Fork(body) {
		return vertexRegionPayGoClassFork
	}
	return vertexRegionOther429Fork
}

func vertexRegionKnownKeysFork(result ojson.Result, fields ...string) bool {
	if !result.IsObject() {
		return false
	}
	seen := make(map[string]bool, len(fields))
	valid := true
	result.ForEach(func(key, _ ojson.Result) bool {
		name := key.String()
		if seen[name] || !slices.Contains(fields, name) {
			valid = false
			return false
		}
		seen[name] = true
		return true
	})
	return valid
}

// An exhausted route is not credential or model quota exhaustion in the
// Manager's generic cooldown ledger.
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

type vertexLocationEvidenceFork interface {
	Supports(model, location string, now time.Time) bool
}

// NewGeminiVertexExecutorWithLocationsFork binds the service-owned inventory
// to the ordinary Vertex executor without consulting it during auth setup.
func NewGeminiVertexExecutorWithLocationsFork(cfg *config.Config, inventory *vertexlocations.Inventory) *GeminiVertexExecutor {
	return &GeminiVertexExecutor{cfg: cfg, vertexLocations: inventory}
}

func verifiedVertexRegionsFork(locations []string, model string, now time.Time, evidence vertexLocationEvidenceFork) []string {
	if len(locations) < 2 || evidence == nil {
		return nil
	}
	out := []string{locations[0]}
	for _, location := range locations[1:] {
		if evidence.Supports(model, location, now) {
			out = append(out, location)
		}
	}
	return out
}

func (e *GeminiVertexExecutor) doVertexRegionRequestFork(client *http.Client, req *http.Request, auth *cliproxyauth.Auth, opts cliproxyexecutor.Options, project, model, primary, action string, info helps.UpstreamRequestLog) (*http.Response, bool, error) {
	record := func(attempt *http.Request) {
		info.URL = attempt.URL.String()
		helps.RecordAPIRequest(req.Context(), e.cfg, info)
	}
	locations := vertexRegionLocationsFork(auth, model, primary)
	configured := len(locations)
	now := time.Now()
	// Read one immutable snapshot for the ordered plan, then recheck the
	// latest positive evidence for each secondary admission below.
	evidence := e.vertexLocations
	inventoryUnavailable := evidence == nil
	if inventory, ok := evidence.(*vertexlocations.Inventory); ok {
		snapshot := inventory.Current(now)
		inventoryUnavailable = snapshot == nil
		evidence = snapshot
	}
	locations = verifiedVertexRegionsFork(locations, model, now, evidence)
	if len(locations) < 2 || !vertexRegionProtocolFork(opts) || !vertexRegionRequestEligibleFork(req, project, model, primary, action) || !vertexRegionPlainTextFork(info.Body) {
		record(req)
		resp, err := client.Do(req)
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			reason := "request_not_eligible"
			switch {
			case configured < 2:
				reason = "no_configured_secondary"
				if auth != nil {
					if mode, _ := vertexlocations.CredentialModeFork(auth.Metadata); mode == vertexlocations.ModeOffFork {
						reason = "policy_off"
					}
				}
			case inventoryUnavailable:
				reason = "inventory_unavailable"
			case len(locations) < 2:
				reason = "no_verified_secondary"
			}
			verifiedModel := ""
			if len(locations) >= 2 {
				verifiedModel = model
			}
			trace := newVertexRegionTraceFork(req.Context(), verifiedModel, primary)
			trace.active, trace.attempts = true, 1
			trace.finish(reason, resp.StatusCode)
		}
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
	router := vertexRegionRouterFork
	mode, explicit := vertexlocations.CredentialModeFork(auth.Metadata)
	router.automatic = explicit && mode == vertexlocations.ModeAutomaticFork
	router.trace = newVertexRegionTraceFork(req.Context(), model, primary)
	router.secondaryAllowed = func(location string, now time.Time) bool {
		return e.vertexLocations.Supports(model, location, now)
	}
	return router.do(client, req, project, model, action, locations, retries, maxWait, record, recordCapacity)
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

// The boolean result says whether the caller must stop native retries.
func (p vertexRegionRouteFork) do(original *http.Client, req *http.Request, project, model, action string, locations []string, retries int, maxWait time.Duration, record func(*http.Request), recordCapacity func(*http.Response, []byte)) (*http.Response, bool, error) {
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
	retryableSeen := false
	handoff := false
	var payGoFloor time.Time
	var payGoReady map[string]time.Time
	phase := 1
	outcome, terminalStatus := "capacity_exhausted", http.StatusTooManyRequests
	defer func() { p.trace.finish(outcome, terminalStatus) }()
	attempt := func(location string, firstPrimary bool) (*http.Response, bool, error) {
		if err := ctx.Err(); err != nil {
			outcome, terminalStatus = "interrupted", 0
			return nil, false, err
		}
		now := p.now()
		if payGoFloor.After(now) {
			return nil, false, nil
		}
		if payGoReady[location].After(now) {
			p.trace.skip(location, phase, "request_local_cooldown")
			return nil, false, nil
		}
		if location != locations[0] && p.secondaryAllowed != nil && !p.secondaryAllowed(location, now) {
			p.trace.skip(location, phase, "verification_unavailable")
			return nil, false, nil
		}
		key := helps.VertexRegionKeyFork{Project: project, Model: model, Location: location}
		finish, until, recovering := p.cooldown.Claim(key, now)
		if finish == nil {
			reason := "in_flight"
			if !until.IsZero() {
				reason = "cooldown"
			}
			p.trace.skip(location, phase, reason)
			if !until.IsZero() {
				retryableSeen = true
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
			outcome, terminalStatus = "local_prepare_error", 0
			return nil, false, err
		}
		attemptReq := req.Clone(ctx)
		attemptReq.URL.Scheme, attemptReq.URL.Host = target.Scheme, target.Host
		attemptReq.URL.Path = vertexRegionPathFork(project, model, location, action)
		attemptReq.Host = target.Host
		attemptReq.Body, err = req.GetBody()
		if err != nil {
			outcome, terminalStatus = "local_prepare_error", 0
			return nil, false, err
		}
		record(attemptReq)
		resp, err := client.Do(attemptReq)
		if err != nil {
			outcome, terminalStatus = "transport_error", 0
			p.trace.attempt(location, phase, terminalStatus, outcome)
			return resp, true, err
		}
		terminalStatus = resp.StatusCode
		if resp.StatusCode != http.StatusTooManyRequests {
			outcome = "upstream_response"
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				outcome = "upstream_success"
				if retryableSeen {
					outcome = "recovered"
				}
			}
			p.trace.attempt(location, phase, terminalStatus, outcome)
			return resp, true, nil
		}
		prefix, errRead := io.ReadAll(io.LimitReader(resp.Body, vertexRegionCapacityMaxFork+1))
		class := vertexRegionOther429Fork
		if errRead == nil {
			class = classifyVertexRegion429Fork(prefix)
		}
		if class != vertexRegionCapacityClassFork && !(p.automatic && class == vertexRegionPayGoClassFork) {
			// Preserve the response, including the legacy first-primary handoff.
			handoff = firstPrimary && !recovering && class == vertexRegionPayGoClassFork
			outcome = "429_not_regional_capacity"
			if handoff {
				outcome = "generic_429_native_handoff"
			}
			p.trace.attempt(location, phase, terminalStatus, outcome)
			resp.Body = vertexRegionPrefixedBodyFork{io.MultiReader(bytes.NewReader(prefix), resp.Body), resp.Body}
			return resp, true, nil
		}
		_ = resp.Body.Close()
		retryableSeen = true
		now = p.now()
		var delay time.Duration
		if header := helps.ParseGoogleRetryAfter(resp.StatusCode, resp.Header, nil, now); header != nil {
			delay = max(delay, *header)
		}
		if body := helps.ParseGoogleRetryAfter(resp.StatusCode, nil, prefix, now); body != nil {
			delay = max(delay, *body)
		}
		if class == vertexRegionPayGoClassFork {
			outcome = "paygo_retry_exhausted"
			p.trace.attempt(location, phase, terminalStatus, "generic_paygo_429")
			// A generic PayGo refusal says nothing about other requests.
			// Keep the location cooldown local, and honor explicit server
			// guidance before trying any location on this request.
			if payGoReady == nil {
				payGoReady = make(map[string]time.Time)
			}
			payGoReady[location] = now.Add(vertexRegionMinCooldownFork)
			if until := now.Add(delay); delay > 0 && until.After(payGoFloor) {
				payGoFloor = until
			}
		} else {
			finish(now.Add(max(vertexRegionMinCooldownFork, delay)))
			finish = nil
			p.trace.attempt(location, phase, terminalStatus, "regional_capacity_429")
		}
		recordCapacity(resp, prefix)
		lastErr = newGeminiStatusErr(resp.StatusCode, resp.Header, prefix)
		return nil, true, nil
	}

	earliestReady := func(now time.Time) time.Time {
		var earliest time.Time
		for _, location := range locations {
			if location != locations[0] && p.secondaryAllowed != nil && !p.secondaryAllowed(location, now) {
				continue
			}
			due := p.cooldown.Ready(helps.VertexRegionKeyFork{Project: project, Model: model, Location: location}, now)
			if local := payGoReady[location]; local.After(due) {
				due = local
			}
			if payGoFloor.After(due) {
				due = payGoFloor
			}
			if earliest.IsZero() || due.Before(earliest) {
				earliest = due
			}
		}
		return earliest
	}

	sweepComplete := true
	for index, location := range locations {
		if err := ctx.Err(); err != nil {
			outcome, terminalStatus = "interrupted", 0
			return nil, true, err
		}
		now := p.now()
		if payGoFloor.After(now) {
			if !payGoFloor.Before(waitLimit) {
				outcome = "retry_hint_exceeds_budget"
				sweepComplete = false
				break
			}
			if err := p.wait(ctx, payGoFloor.Sub(now)); err != nil {
				outcome, terminalStatus = "interrupted", 0
				return nil, true, err
			}
			now = p.now()
			if now.Before(payGoFloor) || !now.Before(waitLimit) {
				outcome = "retry_hint_not_reached"
				if !now.Before(waitLimit) {
					outcome = "retry_hint_exceeds_budget"
				}
				sweepComplete = false
				break
			}
		}
		if resp, _, err := attempt(location, index == 0); resp != nil || err != nil {
			return resp, !handoff, err
		}
	}
	phase = 2
	// Each Phase 2 iteration can issue at most one new HTTP request. Skip a
	// cooldown atomically again after waiting because another request may
	// have refreshed it. The fixed loop bound also limits contention waits.
	for retry := 0; sweepComplete && retryableSeen && retry < retries; retry++ {
		now := p.now()
		ready := now.Add(p.delay(retry))
		if earliest := earliestReady(now); earliest.After(ready) {
			ready = earliest
		}
		if !ready.Before(waitLimit) {
			if payGoFloor.After(now) && !payGoFloor.Before(waitLimit) {
				outcome = "retry_hint_exceeds_budget"
			}
			break
		}
		if err := p.wait(ctx, ready.Sub(now)); err != nil {
			outcome, terminalStatus = "interrupted", 0
			return nil, true, err
		}
		if !p.now().Before(waitLimit) {
			break
		}
		for _, location := range locations {
			resp, sent, err := attempt(location, false)
			if resp != nil || err != nil {
				return resp, true, err
			}
			if sent {
				break
			}
		}
	}
	if err := ctx.Err(); err != nil {
		outcome, terminalStatus = "interrupted", 0
		return nil, true, err
	}
	if lastErr.code == 0 {
		outcome = "no_sendable_candidate"
		lastErr = statusErr{code: http.StatusTooManyRequests, msg: "vertex approved endpoints are cooling down"}
	}
	// The public hint is when any approved endpoint can be tried again,
	// subject to any request-wide PayGo hint still in effect.
	lastErr.retryAfter = nil
	now := p.now()
	if delay := earliestReady(now).Sub(now); delay > 0 {
		lastErr.retryAfter = &delay
	}
	return nil, true, vertexRegionCapacityFailureFork{lastErr}
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
