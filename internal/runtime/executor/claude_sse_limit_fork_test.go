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
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestClaudeExecutorSSEEventLimitFork(t *testing.T) {
	for _, format := range []sdktranslator.Format{sdktranslator.FormatClaude, sdktranslator.FormatOpenAI} {
		t.Run(string(format), func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_limit\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-opus-5\",\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
				// Every line is small enough for Scanner. The event never terminates.
				line := ":" + strings.Repeat("x", 1022) + "\n"
				for sent := 0; sent <= helps.MaxClaudeSSEEventBytes; sent += len(line) {
					if _, err := fmt.Fprint(w, line); err != nil {
						return
					}
				}
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			pluginName := "test-claude-sse-limit-" + string(format)
			plugin := &captureClaudeUsagePlugin{records: make(chan usage.Record, 4)}
			usage.RegisterNamedPlugin(pluginName, plugin)
			defer usage.RegisterNamedPlugin(pluginName, noopClaudeUsagePlugin{})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			exec := NewClaudeExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "key-123", "base_url": server.URL}}
			result, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{
				Model:   "claude-opus-5",
				Payload: []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}],"stream":true}`),
			}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, ResponseFormat: format})
			if err != nil {
				t.Fatal(err)
			}
			errors := 0
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					errors++
					status, ok := chunk.Err.(interface{ StatusCode() int })
					if !ok || status.StatusCode() != http.StatusBadGateway {
						t.Errorf("stream failure status = %v", chunk.Err)
					}
					if chunk.Err.Error() != "upstream SSE event exceeds maximum size" {
						t.Errorf("unexpected stream failure: %v", chunk.Err)
					}
				}
				for _, line := range strings.Split(string(chunk.Payload), "\n") {
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if gjson.Get(payload, "choices.0.finish_reason").String() != "" {
						t.Error("oversized event produced a successful finish reason")
					}
				}
				if strings.Contains(string(chunk.Payload), "message_stop") || strings.Contains(string(chunk.Payload), "[DONE]") {
					t.Error("oversized event produced a successful terminal marker")
				}
			}
			if errors != 1 || ctx.Err() != nil {
				t.Fatalf("expected immediate size rejection, errors = %d, context = %v", errors, ctx.Err())
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("upstream body was not closed after overflow")
			}
			select {
			case record := <-plugin.records:
				if !record.Failed || record.Fail.StatusCode != http.StatusBadGateway || record.Detail.InputTokens != 10 || record.Detail.OutputTokens != 1 {
					t.Fatalf("buffered usage was not retained as failed: %+v", record)
				}
			case <-ctx.Done():
				t.Fatal("overflow did not publish failed usage")
			}
		})
	}
}
