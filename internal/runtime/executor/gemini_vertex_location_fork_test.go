package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestVertexServiceAccountRequestsUseCurrentLocation(t *testing.T) {
	const tokenURL = "https://oauth2.googleapis.com/token"
	var serviceAccount map[string]any
	if err := json.Unmarshal(testVertexServiceAccountJSON(t, tokenURL), &serviceAccount); err != nil {
		t.Fatal(err)
	}
	auth := &coreauth.Auth{ID: "vertex-location-test", Provider: "vertex", Metadata: map[string]any{
		"project_id": "proxy-test", "service_account": serviceAccount,
	}}
	executor := NewGeminiVertexExecutor(&config.Config{})
	req := cliproxyexecutor.Request{
		Model:   "gemini-2.5-flash",
		Payload: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatGemini}

	for _, location := range []struct{ id, host string }{
		{"global", "aiplatform.googleapis.com"},
		{"us", "aiplatform.us.rep.googleapis.com"},
		{"eu", "aiplatform.eu.rep.googleapis.com"},
		{"europe-west4", "europe-west4-aiplatform.googleapis.com"},
	} {
		t.Run(location.id, func(t *testing.T) {
			auth.Metadata["location"] = location.id
			var requests []string
			rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, `{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`
				if req.URL.String() != tokenURL {
					requests = append(requests, req.URL.String())
					status, body = http.StatusBadRequest, `{"error":{"message":"mock model response"}}`
				}
				return &http.Response{
					StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: req,
				}, nil
			})
			ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", rt)
			// The upstream stub rejects model calls after recording their URLs.
			// This test verifies routing, not upstream response translation.
			_, _ = executor.Execute(ctx, auth, req, opts)
			_, _ = executor.ExecuteStream(ctx, auth, req, opts)
			_, _ = executor.CountTokens(ctx, auth, req, opts)
			if len(requests) != 3 {
				t.Fatalf("model requests = %v, want generate, stream and count", requests)
			}
			for i, action := range []string{"generateContent", "streamGenerateContent?alt=sse", "countTokens"} {
				want := fmt.Sprintf("https://%s/v1/projects/proxy-test/locations/%s/publishers/google/models/gemini-2.5-flash:%s", location.host, location.id, action)
				if requests[i] != want {
					t.Fatalf("request %d = %q, want %q", i, requests[i], want)
				}
			}
		})
	}
}
