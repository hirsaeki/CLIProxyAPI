package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestAntigravitySSEPermissionStateFork(t *testing.T) {
	const tosDetails = `,"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"TOS_VIOLATION"}]`
	for _, tc := range []struct {
		name       string
		frame      string
		wantStatus int
		wantPause  bool
	}{
		{"rpc_tos", `{"error":{"code":7,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 403, true},
		{"rpc_tos_no_status", `{"error":{"code":7` + tosDetails + `}}`, 403, true},
		{"http_tos", `{"error":{"code":403,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 403, true},
		{"rpc_validation", `{"error":{"code":7,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"VALIDATION_REQUIRED"}]}}`, 403, false},
		{"rpc_permission", `{"error":{"code":7,"status":"PERMISSION_DENIED"}}`, 403, false},
		{"null_status", `{"error":{"code":7,"status":null` + tosDetails + `}}`, 502, false},
		{"empty_status", `{"error":{"code":7,"status":""` + tosDetails + `}}`, 502, false},
		{"numeric_status", `{"error":{"code":7,"status":0` + tosDetails + `}}`, 502, false},
		{"object_status", `{"error":{"code":7,"status":{}` + tosDetails + `}}`, 502, false},
		{"noncanonical_status", `{"error":{"code":7,"status":" permission_denied "` + tosDetails + `}}`, 502, false},
		{"string_code", `{"error":{"code":"7","status":"PERMISSION_DENIED"` + tosDetails + `}}`, 502, false},
		{"fractional_code", `{"error":{"code":7.5,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 502, false},
		{"decimal_code", `{"error":{"code":7.0,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 502, false},
		{"null_code", `{"error":{"code":null,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 502, false},
		{"invalid_json_code", `{"error":{"code":7x,"status":"PERMISSION_DENIED"` + tosDetails + `}}`, 0, false},
		{"invalid_json_status", `{"error":{"code":7,"status":garbled` + tosDetails + `}}`, 0, false},
		{"conflicting_status", `{"error":{"code":7,"status":"INTERNAL"` + tosDetails + `}}`, 502, false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				resetAntigravityCreditsRetryState()
				t.Cleanup(resetAntigravityCreditsRetryState)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: %s\n\n", tc.frame)
				}))
				defer server.Close()
				cfg := &config.Config{}
				cfg.Antigravity.TOSPauseWindow = "1h"
				exec := NewAntigravityExecutor(cfg)
				auth := newIssue7753AntigravityAuth(t.Name(), server.URL)
				t.Cleanup(func() { closeAntigravityAuthIdleTransports(auth) })
				req := cliproxyexecutor.Request{Model: "claude-sonnet-4-6", Payload: []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`)}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity, ResponseFormat: sdktranslator.FormatAntigravity}
				var err error
				if stream {
					req.Model = "gemini-3-flash"
					result, errStream := exec.ExecuteStream(context.Background(), auth, req, opts)
					err = errStream
					if err == nil && result != nil {
						for chunk := range result.Chunks {
							if chunk.Err != nil {
								err = chunk.Err
							}
						}
					}
				} else {
					_, err = exec.Execute(context.Background(), auth, req, opts)
				}
				var status statusErr
				// A malformed frame is discarded. When it is the only frame, the
				// empty stream must fail without classifying it as a ToS violation.
				if tc.wantStatus == 0 {
					if !errors.As(err, &status) || status.StatusCode() != http.StatusBadGateway ||
						err.Error() != "upstream stream closed with zero output" || status.tosViolation || status.credentialScoped {
						t.Fatalf("malformed-only stream returned %#v, want a non-credential-scoped zero-output 502", err)
					}
					if antigravityInTOSPause(cfg, auth) {
						t.Fatal("malformed frame triggered a ToS pause")
					}
					return
				}
				if !errors.As(err, &status) || status.StatusCode() != tc.wantStatus || status.tosViolation != tc.wantPause || status.credentialScoped != (tc.wantPause || tc.name == "rpc_validation") {
					t.Fatalf("got %#v, want status %d with TOS=%t", err, tc.wantStatus, tc.wantPause)
				}
				if paused := antigravityInTOSPause(cfg, auth); paused != tc.wantPause {
					t.Fatalf("TOS pause=%v, want %v", paused, tc.wantPause)
				}
			})
		}
	}
}
