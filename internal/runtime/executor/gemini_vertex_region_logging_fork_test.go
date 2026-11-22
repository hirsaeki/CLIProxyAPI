package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestVertexRegionRoutingLogsFork(t *testing.T) {
	const project, action = "private-test-project", "generateContent"
	for _, tc := range []struct {
		name, outcome string
		statuses      []int
		generic       bool
		retries       int
		wantPhase     int
		automatic     bool
		hint          string
		firstResult   string
	}{
		{name: "primary-success-quiet", statuses: []int{200}},
		{name: "primary-503", outcome: "upstream_response", statuses: []int{503}, wantPhase: 1},
		{name: "primary-transport-error", outcome: "transport_error", statuses: []int{0}, wantPhase: 1},
		{name: "generic-hands-off", outcome: "generic_429_native_handoff", statuses: []int{429}, generic: true, wantPhase: 1, firstResult: "generic_429_native_handoff"},
		{name: "sweep-recovers", outcome: "recovered", statuses: []int{429, 200}, wantPhase: 1, firstResult: "regional_capacity_429"},
		{name: "exhausted", outcome: "capacity_exhausted", statuses: []int{429, 429}, wantPhase: 1},
		{name: "phase-two-recovers", outcome: "recovered", statuses: []int{429, 429, 200}, retries: 1, wantPhase: 2},
		{name: "alternate-transport-error", outcome: "transport_error", statuses: []int{429, 0}, wantPhase: 1},
		{name: "alternate-local-error", outcome: "local_prepare_error", statuses: []int{429}, wantPhase: 1},
		{name: "paygo-sweep-recovers", outcome: "recovered", statuses: []int{429, 200}, automatic: true, generic: true, wantPhase: 1, firstResult: "generic_paygo_429"},
		{name: "paygo-hint-cuts-sweep", outcome: "retry_hint_exceeds_budget", statuses: []int{429}, automatic: true, generic: true, hint: "20", wantPhase: 1, firstResult: "generic_paygo_429"},
		{name: "paygo-hint-cuts-phase-two", outcome: "retry_hint_exceeds_budget", statuses: []int{429, 429}, automatic: true, generic: true, hint: "20", retries: 1, wantPhase: 1, firstResult: "generic_paygo_429"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, hook := logtest.NewNullLogger()
			now := time.Unix(100, 0)
			p := vertexRegionRouteFork{
				automatic: tc.automatic,
				cooldown:  &helps.VertexRegionCooldownFork{},
				now:       func() time.Time { return now },
				wait:      func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil },
				delay:     func(int) time.Duration { return time.Second },
				trace:     &vertexRegionTraceFork{logger: logger.WithField("model", mockVertexModelFork), primary: "global"},
			}
			sent := 0
			client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				_ = r.Body.Close()
				if sent >= len(tc.statuses) {
					t.Fatal("unexpected upstream attempt")
				}
				status := tc.statuses[sent]
				sent++
				if status == 0 {
					return nil, fmt.Errorf("test-private-transport-error")
				}
				body := "test-private-success-body"
				if status == 429 {
					body = mockCapacityFork
					if tc.generic {
						body = mockGeneric429Fork
					}
				}
				headers := http.Header{"X-Test-Private": []string{"test-private-header"}}
				if tc.hint != "" && sent == len(tc.statuses) {
					headers.Set("Retry-After", tc.hint)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: headers}, nil
			})}
			req, err := http.NewRequest(http.MethodPost, vertexBaseURL("global")+vertexRegionPathFork(project, mockVertexModelFork, "global", action), strings.NewReader(mockVertexBodyFork))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test-private-token")
			if tc.outcome == "local_prepare_error" {
				getBody := req.GetBody
				req.GetBody = func() (io.ReadCloser, error) {
					if sent > 0 {
						return nil, fmt.Errorf("test-private-local-error")
					}
					return getBody()
				}
			}
			resp, stop, routeErr := p.do(client, req, project, mockVertexModelFork, action, []string{"global", "us"}, tc.retries, 8*time.Second, func(*http.Request) {}, func(*http.Response, []byte) {})
			if tc.name == "primary-503" && (routeErr != nil || resp == nil || resp.StatusCode != 503) {
				t.Fatalf("primary error response changed: response=%v err=%v", resp, routeErr)
			}
			if tc.name == "primary-transport-error" && (resp != nil || routeErr == nil || !strings.Contains(routeErr.Error(), "test-private-transport-error")) {
				t.Fatalf("primary transport result changed: response=%v err=%v", resp, routeErr)
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
			events := hook.AllEntries()
			if tc.outcome == "" {
				if len(events) != 0 {
					t.Fatalf("normal success logged %d diagnostic events", len(events))
				}
				return
			}
			for _, entry := range events {
				line := fmt.Sprint(entry.Message, entry.Data)
				if strings.Contains(line, "test-private-") || strings.Contains(line, project) || strings.Contains(line, "aiplatform.googleapis.com") {
					t.Fatal("diagnostic included private sentinel data")
				}
			}
			if len(events) != sent+1 || stop != (tc.automatic || !tc.generic) {
				t.Fatalf("diagnostic count=%d, sent=%d, stop native=%v", len(events), sent, stop)
			}
			if tc.firstResult != "" && events[0].Data["result"] != tc.firstResult {
				t.Fatalf("first attempt result=%v, want %q", events[0].Data["result"], tc.firstResult)
			}
			terminal := events[len(events)-1]
			lastAttempt := events[len(events)-2]
			wantTerminalStatus := tc.statuses[sent-1]
			if tc.outcome == "local_prepare_error" {
				wantTerminalStatus = 0
			}
			if terminal.Level > log.InfoLevel || terminal.Message != "vertex regional routing finished" ||
				terminal.Data["reason"] != tc.outcome || terminal.Data["attempts"] != sent ||
				terminal.Data["status"] != wantTerminalStatus || lastAttempt.Message != "vertex regional HTTP attempt" ||
				lastAttempt.Data["phase"] != tc.wantPhase || lastAttempt.Data["attempt"] != sent {
				t.Fatalf("unexpected attempt/terminal: %v / %v", lastAttempt, terminal)
			}
		})
	}
}

func TestVertexRegionBypassed429LogsFork(t *testing.T) {
	logger := log.StandardLogger()
	previous := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(previous) })
	hook := logtest.NewGlobal()
	const project, action, requestID = "test-private-project", "generateContent", "01969287-6c54-7121-8fa2-ec8903556ce8"
	for _, tc := range []struct {
		mode, reason string
		status       int
		fresh        bool
	}{
		{"off", "policy_off", 429, false},
		{"automatic", "inventory_unavailable", 429, false},
		{"automatic", "no_verified_secondary", 429, true},
		{"automatic", "", 200, false},
	} {
		hook.Reset()
		ctx := logging.WithRequestID(context.Background(), requestID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, vertexBaseURL("global")+vertexRegionPathFork(project, mockVertexModelFork, "global", action), strings.NewReader(mockVertexBodyFork))
		if err != nil {
			t.Fatal(err)
		}
		auth := &cliproxyauth.Auth{Metadata: map[string]any{vertexlocations.ModeKeyFork: tc.mode}}
		exec := NewGeminiVertexExecutorWithLocationsFork(nil, &vertexlocations.Inventory{})
		if tc.fresh {
			exec.vertexLocations = testVertexEvidenceFork(func(_, _ string, _ time.Time) bool { return false })
		}
		client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_ = r.Body.Close()
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("test-private-body")), Header: http.Header{"Retry-After": []string{"17"}}}, nil
		})}
		resp, stop, err := exec.doVertexRegionRequestFork(client, req, auth, cliproxyexecutor.Options{}, project, mockVertexModelFork, "global", action, helps.UpstreamRequestLog{})
		if err != nil || stop || resp == nil || resp.StatusCode != tc.status {
			t.Fatalf("native response not preserved: status=%d, stop=%t, err=%v", tc.status, stop, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || string(body) != "test-private-body" || resp.Header.Get("Retry-After") != "17" {
			t.Fatal("native body or headers not preserved")
		}
		events := hook.AllEntries()
		if tc.reason == "" {
			if len(events) != 0 {
				t.Fatalf("normal native success logged %d events", len(events))
			}
			continue
		}
		if len(events) != 1 || events[0].Data["reason"] != tc.reason || events[0].Data["request_id"] != requestID ||
			events[0].Data["primary_region"] != "global" || events[0].Data["status"] != tc.status || events[0].Data["model"] != nil {
			t.Fatalf("native 429 diagnostic not reported: %v", events)
		}
	}
}

func TestVertexRegionDiagnosticRejectsUntrustedContextIDFork(t *testing.T) {
	ctx := logging.WithRequestID(context.Background(), "private-embedding-identifier")
	trace := newVertexRegionTraceFork(ctx, "gemini-test", "global")
	if data := trace.entry().Data; data["request_id"] != nil {
		t.Fatal("non-CPA request ID emitted")
	}
}
