package executor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// Multi-line usage must be observed before the fork releases a large event buffer.
func TestClaudeSSEMultilineUsageSurvivesBufferResetFork(t *testing.T) {
	for _, format := range []sdktranslator.Format{sdktranslator.FormatClaude, sdktranslator.FormatOpenAI} {
		for _, eof := range []bool{false, true} {
			for _, complete := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/eof=%v/complete=%v", format, eof, complete), func(t *testing.T) {
					// Force ResetClaudeSSEBuffer's large-capacity release path, not only Reset.
					padding := ":" + strings.Repeat("x", 70*1024) + "\n"
					start := padding + "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fork_usage\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-opus-5\",\n" +
						"data: \"usage\":{\"input_tokens\":13,\"output_tokens\":1}}}\n\n"
					delta := padding + "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\n" +
						"data: \"usage\":{\"output_tokens\":17}}"
					stream := start + delta
					if complete {
						stream += "\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}"
					}
					if !eof {
						stream += "\n\n"
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprint(w, stream)
					}))
					defer server.Close()
					pluginName := "test-claude-fork-reset-" + t.Name()
					plugin := &captureClaudeUsagePlugin{records: make(chan usage.Record, 4)}
					usage.RegisterNamedPlugin(pluginName, plugin)
					t.Cleanup(func() { usage.RegisterNamedPlugin(pluginName, noopClaudeUsagePlugin{}) })
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					executor := NewClaudeExecutor(&config.Config{})
					auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
					result, err := executor.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "claude-opus-5", Payload: []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}],"stream":true}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, ResponseFormat: format})
					if err != nil {
						t.Fatal(err)
					}
					failures := 0
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							failures++
						}
					}
					wantFailures := 1
					if complete {
						wantFailures = 0
					}
					if failures != wantFailures || ctx.Err() != nil {
						t.Fatalf("stream errors=%d want=%d context=%v", failures, wantFailures, ctx.Err())
					}
					select {
					case record := <-plugin.records:
						if record.Failed != !complete || record.Detail.InputTokens != 13 || record.Detail.OutputTokens != 17 {
							t.Fatalf("usage lost across event reset/EOF: %+v", record)
						}
						if !complete && record.Fail.StatusCode != http.StatusBadGateway {
							t.Fatalf("failure=%+v want502", record.Fail)
						}
					case <-ctx.Done():
						t.Fatal("usage not published")
					}
				})
			}
		}
	}
}
