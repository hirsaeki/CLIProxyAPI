package executor

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/clienterror"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestMetaResponseDoneFailureFork(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "canceled"} {
		for _, mode := range []string{"stream", "unary-sse", "unary-json"} {
			t.Run(status+"/"+mode, func(t *testing.T) {
				event := fmt.Sprintf(`{"type":"response.done","response":{"id":"resp-failed","object":"response","status":%q,"output":[]}}`, status)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if mode == "unary-json" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, event)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "event: response.done\ndata: %s\n\n", event)
				}))
				defer server.Close()

				exec := NewMetaExecutor(&config.Config{})
				auth := &cliproxyauth.Auth{ID: t.Name(), Provider: "meta", Attributes: map[string]string{"api_key": "test-token", "base_url": server.URL}}
				req := cliproxyexecutor.Request{Model: "muse-wonder-1.0", Payload: []byte(`{"model":"muse-wonder-1.0","input":"hi"}`)}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Stream: mode == "stream"}
				var got error
				if opts.Stream {
					res, err := exec.ExecuteStream(t.Context(), auth, req, opts)
					got = err
					if err == nil {
						for chunk := range res.Chunks {
							if chunk.Err != nil {
								got = chunk.Err
							}
						}
					}
				} else {
					_, got = exec.Execute(t.Context(), auth, req, opts)
				}
				if got == nil || clienterror.HTTPStatusFromError(got) != http.StatusBadGateway || got.Error() != event {
					t.Fatalf("response.done status %q: got %v, want 502 preserving upstream event", status, got)
				}
			})
		}
	}
}
