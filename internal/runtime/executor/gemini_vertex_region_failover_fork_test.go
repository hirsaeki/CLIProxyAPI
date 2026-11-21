package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const (
	mockVertexModelFork = "gemini-4-test"
	mockVertexBodyFork  = `{"contents":[{"role":"user","parts":[{"text":"synthetic request"}]}]}`
	mockGeneric429Fork  = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Resource exhausted. Please try again later."}}`
	mockCapacityFork    = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Temporary regional capacity overload; please try again later."}}`
)

type testVertexEvidenceFork func(string, string, time.Time) bool

func (f testVertexEvidenceFork) Supports(model, location string, now time.Time) bool {
	return f(model, location, now)
}

func TestVerifiedVertexRegionsFork(t *testing.T) {
	now := time.Unix(100, 0)
	evidence := testVertexEvidenceFork(func(model, location string, at time.Time) bool {
		return at.Equal(now) && model == "gemini-exact-preview" && location != "global" && location != "eu"
	})
	allowed := []string{"global", "eu", "europe-west4", "us"}
	got := verifiedVertexRegionsFork(allowed, "gemini-exact-preview", now, evidence)
	if !slices.Equal(got, []string{"global", "europe-west4", "us"}) {
		t.Fatalf("intersection or explicit primary/order = %v", got)
	}
	if got := verifiedVertexRegionsFork(allowed, "gemini-exact", now, evidence); !slices.Equal(got, []string{"global"}) {
		t.Fatalf("inferred model alias location: %v", got)
	}
	if got := verifiedVertexRegionsFork(allowed, "gemini-exact-preview", now.Add(time.Second), evidence); !slices.Equal(got, []string{"global"}) {
		t.Fatalf("expired evidence authorized alternate: %v", got)
	}
}

func TestVertexRegionClassifierAndEligibilityFork(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
		want   bool
	}{
		{429, mockCapacityFork, true},
		{503, mockCapacityFork, false},
		{429, strings.Replace(mockCapacityFork, "Temporary regional capacity overload; please try again later.", "Resource exhausted. Please try again later.", 1), false},
		{429, strings.Replace(mockCapacityFork, "Temporary regional capacity overload", "Regional capacity limit reached", 1), false},
		{429, strings.Replace(mockCapacityFork, `later."}`, `later.","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure"}]}`, 1), false},
		{429, strings.Replace(mockCapacityFork, `later."}`, `later.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXCEEDED"}]}`, 1), false},
	} {
		if got := vertexRegionCapacity429Fork(test.status, []byte(test.body)); got != test.want {
			t.Fatalf("capacity %d %s = %t, want %t", test.status, test.body, got, test.want)
		}
	}
	for _, token := range []string{"global", "us", "eu", "us-central1", "europe-west4", "northamerica-northeast1"} {
		if !vertexRegionLocationCodeFork(token) {
			t.Errorf("valid location rejected: %s", token)
		}
	}
	for _, token := range []string{"GLOBAL", "us central1", "us-west", "fake-city1", "https://example.invalid", "global/../us", "googleapis.com"} {
		if vertexRegionLocationCodeFork(token) {
			t.Errorf("unsafe location accepted: %s", token)
		}
	}
	for _, format := range []sdktranslator.Format{sdktranslator.FormatInteractions, sdktranslator.FormatOpenAIResponse} {
		if vertexRegionProtocolFork(cliproxyexecutor.Options{SourceFormat: format}) {
			t.Errorf("stateful source eligible: %s", format)
		}
	}
	for _, model := range []string{"gemini-3-pro-image", "gemini-3.1-flash-tts", "gemini-3.8-live", "gemini-omni-1.2-flash"} {
		if vertexRegionTextModelFork(model) {
			t.Errorf("non-text model eligible: %s", model)
		}
	}
	if !vertexRegionPlainTextFork([]byte(mockVertexBodyFork)) {
		t.Fatal("ordinary payload not eligible")
	}
	for _, field := range []string{`"cachedContent":"projects/x/locations/us/cachedContents/y"`, `"tools":[{"googleSearch":{}}]`, `"toolConfig":{}`, `"generationConfig":{"responseModalities":["IMAGE"]}`, `"store":true`} {
		body := []byte(strings.Replace(mockVertexBodyFork, "{", "{"+field+",", 1))
		if vertexRegionPlainTextFork(body) {
			t.Fatalf("restricted payload eligible: %s", body)
		}
	}
}

func TestVertexRegionGenericEnvelopeFork(t *testing.T) {
	const retryInfo = `,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1.25s"}]`
	withDetails := func(details string) string {
		return strings.TrimSuffix(mockGeneric429Fork, "}}") + details + "}}"
	}
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"canonical", mockGeneric429Fork, true},
		{"documented-comma", strings.Replace(mockGeneric429Fork, "exhausted. Please", "exhausted, please", 1), true},
		{"documentation-suffix", strings.Replace(mockGeneric429Fork, "later.", "later. Please refer to https://cloud.google.com/vertex-ai/generative-ai/docs/error-code-429 for more details.", 1), true},
		{"retry-info", withDetails(retryInfo), true},
		{"empty-details", withDetails(`,"details":[]`), true},
		{"truncated", mockGeneric429Fork[:len(mockGeneric429Fork)-1], false},
		{"positive-capacity", mockCapacityFork, false},
		{"explicit-quota", strings.Replace(mockGeneric429Fork, "Resource exhausted.", "Quota limit exceeded.", 1), false},
		{"quota-detail", withDetails(`,"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure"}]`), false},
		{"unknown-detail", withDetails(`,"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]`), false},
		{"retry-info-plus-unknown", withDetails(strings.TrimSuffix(retryInfo, "]") + `,{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]`), false},
		{"non-protobuf-duration", withDetails(strings.Replace(retryInfo, "1.25s", "1m1s", 1)), false},
		{"excess-retry-precision", withDetails(strings.Replace(retryInfo, "1.25s", "1.1234567890s", 1)), false},
		{"empty-retry-info", withDetails(strings.Replace(retryInfo, "1.25s", "", 1)), false},
		{"unknown-field", withDetails(`,"reason":"PROJECT_QUOTA_EXCEEDED"`), false},
		{"duplicate-detail", withDetails(`,"details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure"}]` + retryInfo), false},
		{"string-code", strings.Replace(mockGeneric429Fork, "429", `"429"`, 1), false},
	} {
		if got := vertexRegionGeneric429Fork([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: generic = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestVertexRegionGenericOwnershipFork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		wantLoc    string
		stopNative bool
	}{
		{"untouched-primary-handoff", "", "global", false},
		{"active-cooldown-no-handoff", "active", "us", true},
		{"recovery-primary-no-handoff", "expired", "global", true},
		{"in-flight-recovery-no-handoff", "in-flight", "us", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const project = "mock-project"
			const action = "generateContent"
			now := time.Unix(100, 0)
			cooldown := &helps.VertexRegionCooldownFork{}
			key := helps.VertexRegionKeyFork{Project: project, Model: mockVertexModelFork, Location: "global"}
			if tc.state != "" {
				finish, _, _ := cooldown.Claim(key, now.Add(-2*time.Second))
				until := now
				if tc.state == "active" {
					until = now.Add(time.Second)
				}
				finish(until)
				if tc.state == "in-flight" {
					probe, _, _ := cooldown.Claim(key, now)
					defer probe(time.Time{})
				}
			}
			router := vertexRegionRouteFork{
				cooldown: cooldown,
				now:      func() time.Time { return now },
				wait:     func(context.Context, time.Duration) error { t.Fatal("generic 429 entered Phase 2"); return nil },
				delay:    func(int) time.Duration { t.Fatal("generic 429 entered Phase 2"); return 0 },
			}
			var paths []string
			body := " " + mockGeneric429Fork + "\n"
			client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				_ = req.Body.Close()
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"17"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			req, err := http.NewRequest(http.MethodPost, vertexBaseURL("global")+vertexRegionPathFork(project, mockVertexModelFork, "global", action), strings.NewReader(mockVertexBodyFork))
			if err != nil {
				t.Fatal(err)
			}
			resp, stop, err := router.do(client, req, project, mockVertexModelFork, action, []string{"global", "us"}, 4, 8*time.Second, func(*http.Request) {}, func(*http.Response, []byte) {})
			if err != nil || resp == nil || resp.StatusCode != 429 {
				t.Fatalf("response=%v err=%v", resp, err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil || string(data) != body || resp.Header.Get("Retry-After") != "17" {
				t.Fatalf("lost response: %q headers=%v err=%v", data, resp.Header, err)
			}
			if want := []string{vertexRegionPathFork(project, mockVertexModelFork, tc.wantLoc, action)}; !slices.Equal(paths, want) || stop != tc.stopNative {
				t.Fatalf("paths=%q stop=%t, want %q/%t", paths, stop, want, tc.stopNative)
			}
		})
	}
}

func TestVertexRegionRoutingPhasesFork(t *testing.T) {
	for _, tc := range []struct {
		name       string
		locations  []string
		retries    int
		window     time.Duration
		successAt  int
		cancelWait bool
		deadline   bool
		want       []string
		wantWaits  []time.Duration
	}{
		{"full-sweep", []string{"global", "us", "eu", "us-central1", "europe-west4", "europe-west1", "asia-northeast1", "asia-northeast3", "asia-southeast1", "northamerica-northeast1", "us-west1"}, 3, 8 * time.Second, 11, false, false, []string{"global", "us", "eu", "us-central1", "europe-west4", "europe-west1", "asia-northeast1", "asia-northeast3", "asia-southeast1", "northamerica-northeast1", "us-west1"}, nil},
		{"phase-two", []string{"global", "us", "eu"}, 3, 8 * time.Second, 4, false, false, []string{"global", "us", "eu", "us"}, []time.Duration{2 * time.Second}},
		{"exhaustion", []string{"global", "us", "eu"}, 2, 8 * time.Second, 0, false, false, []string{"global", "us", "eu", "us", "us"}, []time.Duration{2 * time.Second, 2 * time.Second}},
		{"wait-budget", []string{"global", "us", "eu"}, 3, time.Second, 0, false, false, []string{"global", "us", "eu"}, nil},
		{"deadline", []string{"global", "us", "eu"}, 3, 8 * time.Second, 0, false, true, []string{"global", "us", "eu"}, nil},
		{"cancel", []string{"global", "us", "eu"}, 3, 8 * time.Second, 0, true, false, []string{"global", "us", "eu"}, []time.Duration{2 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const project = "mock-project"
			const action = "generateContent"
			now := time.Now()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.deadline {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, now.Add(time.Second))
				defer deadlineCancel()
			}
			var waits []time.Duration
			p := vertexRegionRouteFork{
				cooldown: &helps.VertexRegionCooldownFork{},
				now:      func() time.Time { return now },
				wait: func(ctx context.Context, delay time.Duration) error {
					waits = append(waits, delay)
					if tc.cancelWait {
						cancel()
						return ctx.Err()
					}
					now = now.Add(delay)
					return nil
				},
				delay: func(round int) time.Duration { return time.Duration(1<<round) * time.Second },
			}
			var attempts []string
			client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				location := strings.Split(r.URL.Path, "/")[5]
				if r.URL.String() != vertexBaseURL(location)+vertexRegionPathFork(project, mockVertexModelFork, location, action) ||
					r.Host != r.URL.Host || r.Header.Get("Authorization") != "Bearer synthetic" {
					return nil, fmt.Errorf("unexpected synthetic request target or authorization")
				}
				data, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if string(data) != mockVertexBodyFork {
					return nil, fmt.Errorf("request body changed on retry")
				}
				attempts = append(attempts, location)
				if len(attempts) == tc.successAt {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("success"))}, nil
				}
				delay := "7"
				body := mockCapacityFork
				if location == "us" {
					delay = "2"
					// Existing helper prioritizes RetryInfo; the new regional policy
					// must honor the longer conflicting HTTP Retry-After too.
					body = strings.Replace(body, `later."}`, `later.","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1s"}]}`, 1)
				}
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{delay}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, vertexBaseURL("global")+vertexRegionPathFork(project, mockVertexModelFork, "global", action), strings.NewReader(mockVertexBodyFork))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer synthetic")
			resp, stop, err := p.do(client, req, project, mockVertexModelFork, action, tc.locations, tc.retries, tc.window, func(*http.Request) {}, func(*http.Response, []byte) {})
			if !stop {
				t.Fatal("regional capacity routing did not own the request")
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
			if !reflect.DeepEqual(attempts, tc.want) || !reflect.DeepEqual(waits, tc.wantWaits) {
				t.Fatalf("attempts=%v waits=%v, want %v/%v", attempts, waits, tc.want, tc.wantWaits)
			}
			if tc.successAt != 0 {
				if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
					t.Fatalf("successful route response=%v err=%v", resp, err)
				}
			} else if tc.cancelWait {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled backoff err=%v", err)
				}
			} else {
				var limit cliproxyexecutor.StatusError
				var scoped cliproxyexecutor.RequestScopedError
				if !errors.As(err, &limit) || limit.StatusCode() != 429 || !errors.As(err, &scoped) || !scoped.IsRequestScoped() {
					t.Fatalf("exhausted route %+v", err)
				}
			}
		})
	}
}

func TestVertexRegionNativeManagerBoundaryFork(t *testing.T) {
	for i, test := range []struct {
		name   string
		stream bool
		optIn  bool
		mode   string
		want   []string
	}{
		{"unary-last-eligible", false, true, "fallback", []string{"global", "us", "eu"}},
		{"openai-chat-last-eligible", false, true, "fallback", []string{"global", "us", "eu"}},
		{"stream-last-eligible", true, true, "fallback", []string{"global", "us", "eu"}},
		{"catalog-intersection", false, true, "catalog-intersection", []string{"global", "eu"}},
		{"automatic-unary-widens-only-after-opt-in", false, true, "automatic", []string{"global", "europe-west4"}},
		{"automatic-stream-widens-only-after-opt-in", true, true, "automatic", []string{"global", "europe-west4"}},
		{"explicit-off-overrides-legacy", false, true, "explicit-off", []string{"global"}},
		{"automatic-no-inventory", false, true, "cold-catalog", []string{"global"}},
		{"automatic-missing-exact-model", true, true, "unknown-catalog", []string{"global"}},
		{"catalog-order", true, true, "catalog-order", []string{"global", "eu", "us"}},
		{"catalog-revoked-in-flight", false, true, "catalog-revoked", []string{"global", "eu"}},
		{"no-inventory-primary-only", false, true, "cold-catalog", []string{"global"}},
		{"unknown-model-primary-only", true, true, "unknown-catalog", []string{"global"}},
		{"no-outer-unary-retry", false, true, "all", []string{"global", "us", "eu"}},
		{"no-outer-stream-retry", true, true, "all", []string{"global", "us", "eu"}},
		{"primary-generic-unary-opted-out", false, false, "generic", []string{"global", "europe-west4"}},
		{"primary-generic-unary-opted-in", false, true, "generic", []string{"global", "europe-west4"}},
		{"primary-generic-stream-opted-out", true, false, "generic", []string{"global", "europe-west4"}},
		{"primary-generic-stream-opted-in", true, true, "generic", []string{"global", "europe-west4"}},
		{"first-primary-native-timed-retry-opted-out", false, false, "generic-outer", []string{"global", "global"}},
		{"first-primary-native-timed-retry-opted-in", false, true, "generic-outer", []string{"global", "global"}},
		{"after-capacity-generic-unary", false, true, "capacity-then-generic", []string{"global", "us"}},
		{"after-capacity-generic-stream", true, true, "capacity-then-generic", []string{"global", "us"}},
		{"permanent-quota", false, true, "quota", []string{"global"}},
		{"authentication-error", false, true, "auth", []string{"global"}},
		{"redirect", false, true, "redirect", []string{"global"}},
		{"opt-out", false, false, "opted-out-capacity", []string{"global"}},
		{"transport-context-error", false, true, "transport-context-error", []string{"global"}},
		{"http-accepted-before-first-output", true, true, "before", []string{"global"}},
		{"no-replay-after-first-output", true, true, "committed", []string{"global"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := "mock-project-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.Itoa(i)
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/token" {
					t.Errorf("Unexpected token route %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"access_token":"synthetic-shared-auth","token_type":"Bearer","expires_in":3600}`)
			}))
			defer tokenServer.Close()
			sa, ok := ojson.ParseBytes(testVertexServiceAccountJSON(t, tokenServer.URL+"/token")).Value().(map[string]any)
			if !ok {
				t.Fatal("invalid synthetic auth")
			}
			timeout := 5 * time.Second
			if test.mode == "generic-outer" {
				// The native Manager uses a 10s minimum cooldown even
				// with subsecond RetryInfo. Run the parity cases together.
				t.Parallel()
				timeout = 22 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			pipeR, pipeW := io.Pipe()
			defer pipeR.Close()
			defer pipeW.Close()
			var attempts []string
			var revoked bool
			var attemptTimes []time.Time
			var sentBody string
			var stopped = statusErr{code: 429, msg: mockGeneric429Fork}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			ctx = context.WithValue(ctx, "cliproxy.roundtripper", roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == tokenServer.URL+"/token" {
					return transport.RoundTrip(r)
				}
				var location string
				for _, candidate := range []string{"global", "us", "eu", "europe-west4"} {
					if r.URL.Scheme+"://"+r.URL.Host == vertexBaseURL(candidate) {
						location = candidate
					}
				}
				action := "generateContent"
				if test.stream {
					action = "streamGenerateContent"
				}
				if location == "" || r.URL.Path != vertexRegionPathFork(project, mockVertexModelFork, location, action) ||
					r.Header.Get("Authorization") != "Bearer synthetic-shared-auth" {
					t.Errorf("unexpected mock host/path/auth or redirect: %s", r.URL)
					return nil, fmt.Errorf("unexpected mock host/path/auth")
				}
				bytes, _ := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if len(attempts) == 0 {
					sentBody = string(bytes)
				} else if sentBody != string(bytes) {
					return nil, fmt.Errorf("payload changed")
				}
				attempts = append(attempts, location)
				attemptTimes = append(attemptTimes, time.Now())
				resp := &http.Response{StatusCode: 200, Header: http.Header{}}
				const result = `{"candidates":[{"content":{"role":"model","parts":[{"text":"synthetic-output"}]},"finishReason":"STOP"}]}`
				out := result
				switch test.mode {
				case "fallback", "catalog-intersection", "catalog-revoked":
					if test.mode == "catalog-revoked" && location == "global" {
						revoked = true
					}
					if location != "eu" {
						resp.StatusCode, out = 429, mockCapacityFork
					}
				case "automatic":
					if location != "europe-west4" {
						resp.StatusCode, out = 429, mockCapacityFork
					}
				case "catalog-order":
					if location != "us" {
						resp.StatusCode, out = 429, mockCapacityFork
					}
				case "all", "opted-out-capacity", "explicit-off", "cold-catalog", "unknown-catalog":
					resp.StatusCode, out = 429, mockCapacityFork
					resp.Header.Set("Retry-After", "20")
				case "generic", "capacity-then-generic":
					if (location == "global" && test.mode == "generic") || (location == "us" && test.mode == "capacity-then-generic") {
						resp.StatusCode, out = 429, mockGeneric429Fork
						resp.Header.Set("Retry-After", "20")
					} else if location == "global" {
						resp.StatusCode, out = 429, mockCapacityFork
					}
				case "generic-outer":
					if len(attempts) == 1 {
						resp.StatusCode = 429
						out = strings.TrimSuffix(mockGeneric429Fork, "}}") +
							`,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.01s"}]}}`
					}
				case "quota":
					resp.StatusCode, out = 429, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded."}}`
				case "auth":
					resp.StatusCode, out = 401, `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`
				case "transport-context-error":
					return nil, context.DeadlineExceeded
				case "redirect":
					resp.StatusCode = 307
					resp.Header.Set("Location", "https://example.invalid/should-not-send")
				case "before":
					_ = pipeW.CloseWithError(stopped)
					resp.Body = pipeR
					return resp, nil
				case "committed":
					const first = `{"candidates":[{"content":{"role":"model","parts":[{"text":"synthetic-output"}]}}]}`
					go func() { _, _ = io.WriteString(pipeW, "data: "+first+"\n\n") }()
					resp.Body = pipeR
					return resp, nil
				}
				if test.stream && resp.StatusCode == 200 {
					out = "data: " + out + "\n\n"
				}
				resp.Body = io.NopCloser(strings.NewReader(out))
				return resp, nil
			}))
			cfg := &config.Config{RequestRetry: 3, MaxRetryInterval: 4}
			m := cliproxyauth.NewManager(nil, nil, nil)
			if test.mode == "generic-outer" {
				m.SetRetryConfig(1, 16*time.Second, 1)
			} else if (test.optIn && test.mode != "cold-catalog" && test.mode != "unknown-catalog" && test.mode != "explicit-off") || test.mode == "generic" {
				m.SetRetryConfig(3, 4*time.Second, 2)
			} else {
				m.SetRetryConfig(0, 0, 1)
			}
			exec := NewGeminiVertexExecutor(cfg)
			if test.mode != "cold-catalog" {
				exec.vertexLocations = testVertexEvidenceFork(func(model, location string, _ time.Time) bool {
					if model != mockVertexModelFork || test.mode == "unknown-catalog" {
						return false
					}
					switch test.mode {
					case "catalog-intersection":
						return location == "eu" || location == "europe-west4"
					case "automatic":
						return location == "europe-west4"
					case "catalog-revoked":
						return location == "eu" || (location == "us" && !revoked)
					default:
						return true
					}
				})
			}
			m.RegisterExecutor(exec)
			for j, id := range []string{"primary", "other-auth"} {
				if test.mode == "generic-outer" && j > 0 {
					break
				}
				auth := &cliproxyauth.Auth{ID: t.Name() + id, Provider: "vertex", Status: cliproxyauth.StatusActive,
					Attributes: map[string]string{"priority": strconv.Itoa(10 - j*10)},
					Metadata:   map[string]any{"project_id": project, "location": "global", "service_account": sa},
				}
				if j == 0 {
					if strings.HasPrefix(test.name, "automatic-") {
						auth.Metadata[vertexlocations.ModeKeyFork] = vertexlocations.ModeAutomaticFork
					} else if test.mode == "explicit-off" {
						auth.Metadata[vertexlocations.ModeKeyFork] = vertexlocations.ModeOffFork
					}
				}
				if j == 1 && (test.mode == "generic" || test.mode == "capacity-then-generic") {
					// Native alternative auth is deliberately outside the
					// first account's automatic regional allowlist.
					auth.Metadata["location"] = "europe-west4"
				}
				if j == 0 && test.optIn {
					locations := []any{"global", "us", "eu"}
					if test.mode == "catalog-order" {
						locations = []any{"global", "eu", "us"}
					}
					auth.Metadata[vertexRegionPolicyKeyFork] = map[string]any{mockVertexModelFork: locations}
				}
				if test.mode == "generic-outer" {
					auth.Metadata["request_retry"] = 1
				} else if j == 0 && (test.optIn || test.mode == "generic") {
					auth.Metadata["request_retry"] = 0
				}
				if _, err := m.Register(ctx, auth); err != nil {
					t.Fatal(err)
				}
				registry.GetGlobalRegistry().RegisterClient(auth.ID, "vertex", []*registry.ModelInfo{{ID: mockVertexModelFork, Type: "gemini"}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
			}
			req := cliproxyexecutor.Request{Model: mockVertexModelFork, Payload: []byte(mockVertexBodyFork)}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatGemini, ResponseFormat: sdktranslator.FormatGemini, Stream: test.stream}
			if test.name == "openai-chat-last-eligible" {
				req.Payload = []byte(`{"messages":[{"role":"user","content":"synthetic request"}]}`)
				opts.SourceFormat = sdktranslator.FormatOpenAI
				opts.ResponseFormat = sdktranslator.FormatOpenAI
			}
			var runErr error
			var out strings.Builder
			if test.stream {
				var sr *cliproxyexecutor.StreamResult
				sr, runErr = m.ExecuteStream(ctx, []string{"vertex"}, req, opts)
				if sr != nil {
					for chunk := range sr.Chunks {
						if chunk.Err != nil {
							runErr = chunk.Err
						}
						out.Write(chunk.Payload)
						if test.mode == "committed" && strings.Contains(out.String(), "synthetic-output") {
							_ = pipeW.CloseWithError(stopped)
						}
					}
				}
			} else {
				res, err := m.Execute(ctx, []string{"vertex"}, req, opts)
				runErr = err
				out.Write(res.Payload)
			}
			if !slices.Equal(attempts, test.want) {
				t.Fatalf("attempts=%v want=%v err=%v", attempts, test.want, runErr)
			}
			if test.mode == "generic-outer" && attemptTimes[1].Sub(attemptTimes[0]) < 9*time.Second {
				t.Fatal("native retry did not wait for the cooldown floor")
			}
			if test.mode == "fallback" || test.mode == "generic" || test.mode == "generic-outer" ||
				test.mode == "catalog-intersection" || test.mode == "catalog-order" || test.mode == "catalog-revoked" || test.mode == "automatic" {
				if runErr != nil || !strings.Contains(out.String(), "synthetic-output") {
					t.Fatalf("output=%q err=%v", out.String(), runErr)
				}
			} else if runErr == nil {
				t.Fatalf("expected terminal error, output=%q", out.String())
			}
			if (test.mode == "cold-catalog" || test.mode == "unknown-catalog" || test.mode == "explicit-off") && cliproxyauth.IsRequestStopError(runErr) {
				t.Fatal("native primary semantics changed with no eligible secondary")
			}
			if test.mode == "all" {
				state, _ := m.GetByID(t.Name() + "primary")
				if m := state.ModelStates[mockVertexModelFork]; m != nil && !m.NextRetryAfter.IsZero() {
					t.Fatal("regional failure incorrectly cooled native auth/model")
				}
			}
		})
	}
}
