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
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const (
	mockVertexModelFork = "gemini-4-test"
	mockVertexBodyFork  = `{"contents":[{"role":"user","parts":[{"text":"synthetic request"}]}]}`
	mockCapacityFork    = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Temporary regional capacity overload; please try again later."}}`
)

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
			resp, err := p.do(client, req, project, mockVertexModelFork, action, tc.locations, tc.retries, tc.window, func(*http.Request) {}, func(*http.Response, []byte) {})
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
		{"no-outer-unary-retry", false, true, "all", []string{"global", "us", "eu"}},
		{"no-outer-stream-retry", true, true, "all", []string{"global", "us", "eu"}},
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pipeR, pipeW := io.Pipe()
			defer pipeR.Close()
			defer pipeW.Close()
			var attempts []string
			var sentBody string
			var stopped = statusErr{code: 429, msg: mockCapacityFork}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			defer transport.CloseIdleConnections()
			ctx = context.WithValue(ctx, "cliproxy.roundtripper", roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == tokenServer.URL+"/token" {
					return transport.RoundTrip(r)
				}
				var location string
				for _, candidate := range []string{"global", "us", "eu"} {
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
				resp := &http.Response{StatusCode: 200, Header: http.Header{}}
				const result = `{"candidates":[{"content":{"role":"model","parts":[{"text":"synthetic-output"}]},"finishReason":"STOP"}]}`
				out := result
				switch test.mode {
				case "fallback":
					if location != "eu" {
						resp.StatusCode, out = 429, mockCapacityFork
					}
				case "all", "opted-out-capacity":
					resp.StatusCode, out = 429, mockCapacityFork
					resp.Header.Set("Retry-After", "20")
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
			if test.optIn {
				m.SetRetryConfig(3, 4*time.Second, 2)
			} else {
				m.SetRetryConfig(0, 0, 1)
			}
			m.RegisterExecutor(NewGeminiVertexExecutor(cfg))
			for j, id := range []string{"primary", "other-auth"} {
				auth := &cliproxyauth.Auth{ID: t.Name() + id, Provider: "vertex", Status: cliproxyauth.StatusActive,
					Attributes: map[string]string{"priority": strconv.Itoa(10 - j*10)},
					Metadata:   map[string]any{"project_id": project, "location": "global", "service_account": sa},
				}
				if j == 0 && test.optIn {
					auth.Metadata[vertexRegionPolicyKeyFork] = map[string]any{mockVertexModelFork: []any{"global", "us", "eu"}}
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
			if test.mode == "fallback" {
				if runErr != nil || !strings.Contains(out.String(), "synthetic-output") {
					t.Fatalf("output=%q err=%v", out.String(), runErr)
				}
			} else if runErr == nil {
				t.Fatalf("expected terminal error, output=%q", out.String())
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
