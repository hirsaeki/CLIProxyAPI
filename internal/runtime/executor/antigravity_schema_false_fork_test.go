package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	antigravityclaude "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/antigravity/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/sjson"
)

func TestAntigravityRejectsFalseSchemaBeforeSendingFork(t *testing.T) {
	e := &AntigravityExecutor{}
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"project_id": "project-1"}}
	payload := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"blocked","parameters":{"type":"object","properties":{"x":{"anyOf":[false]}}}}]}]}}`)
	for _, model := range []string{"claude-opus-5-5-high", "gemini-3.6-flash-high"} {
		for _, stream := range []bool{false, true} {
			req, err := e.buildRequest(context.Background(), auth, "token", model, payload, stream, "", antigravityBaseURLDaily)
			if req != nil {
				t.Fatal("constructed a request carrying an unsupported schema")
			}
			status, ok := err.(interface{ StatusCode() int })
			if !ok || status.StatusCode() != http.StatusBadRequest {
				t.Fatalf("model %s stream %t: want local 400, got %v", model, stream, err)
			}
		}
	}

	claudePayload := []byte(`{"model":"claude-opus-5-5-high","messages":[{"role":"user","content":"hi"}],"tools":[{"name":"blocked","input_schema":{"anyOf":[false]}}]}`)
	translated, errTranslate := antigravityclaude.ConvertClaudeRequestToAntigravity("claude-opus-5-5-high", claudePayload, false)
	if errTranslate != nil {
		t.Fatal(errTranslate)
	}
	if req, err := e.buildRequest(context.Background(), auth, "token", "claude-opus-5-5-high", translated, false, "", antigravityBaseURLDaily); req != nil || err == nil {
		t.Fatalf("translator/executor chain accepted an all-false root schema: %v", err)
	}
}

func TestAntigravityFalseSchemaChecksFinalPayloadFork(t *testing.T) {
	e := &AntigravityExecutor{}
	auth := &cliproxyauth.Auth{Metadata: map[string]any{"project_id": "project-1"}}
	payload := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`)
	ctx := helps.WithPayloadFinalizer(context.Background(), func(body []byte) []byte {
		updated, err := sjson.SetRawBytes(body, "request.generationConfig.responseSchema", []byte(`{"anyOf":[false]}`))
		if err != nil {
			t.Fatal(err)
		}
		return updated
	})
	if req, err := e.buildRequest(ctx, auth, "token", "gemini-3.6-flash-high", payload, false, "", antigravityBaseURLDaily); req != nil || err == nil {
		t.Fatalf("payload rules bypassed native schema validation: %v", err)
	}
	ctxNested := helps.WithPayloadFinalizer(context.Background(), func(body []byte) []byte {
		updated, err := sjson.SetRawBytes(body, "request.generationConfig.responseSchema", []byte(`{"contains":{"anyOf":[false]}}`))
		if err != nil {
			t.Fatal(err)
		}
		return updated
	})
	if req, err := e.buildRequest(ctxNested, auth, "token", "gemini-3.6-flash-high", payload, false, "", antigravityBaseURLDaily); req != nil || err == nil {
		t.Fatalf("payload rules bypassed nested native schema validation: %v", err)
	}
	blocked, errSet := sjson.SetRawBytes(payload, "request.generationConfig.responseSchema", []byte(`{"anyOf":[false]}`))
	if errSet != nil {
		t.Fatal(errSet)
	}
	ctx = helps.WithPayloadFinalizer(context.Background(), func(body []byte) []byte {
		updated, err := sjson.DeleteBytes(body, "request.generationConfig.responseSchema")
		if err != nil {
			t.Fatal(err)
		}
		return updated
	})
	req, err := e.buildRequest(ctx, auth, "token", "gemini-3.6-flash-high", blocked, false, "", antigravityBaseURLDaily)
	if err != nil || req == nil {
		t.Fatalf("payload rules could not remove an unsupported schema: %v", err)
	}
	if errClose := req.Body.Close(); errClose != nil {
		t.Fatal(errClose)
	}
}

func TestAntigravityCountTokensRejectsFalseSchemaFork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"totalTokens":1}`))
	}))
	defer server.Close()
	e := &AntigravityExecutor{}
	auth := &cliproxyauth.Auth{
		ID:         "false-schema-test",
		Provider:   "antigravity",
		Attributes: map[string]string{"base_url": server.URL},
		Metadata: map[string]any{
			"access_token": "token",
			"project_id":   "project-1",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	payload := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"blocked","parameters":{"anyOf":[false]}}]}]}}`)
	_, err := e.CountTokens(context.Background(), auth, cliproxyexecutor.Request{Model: "gemini-3.6-flash-high", Payload: payload}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity})
	status, ok := err.(interface{ StatusCode() int })
	if !ok || status.StatusCode() != http.StatusBadRequest {
		t.Fatalf("want local 400, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unsupported schema reached upstream %d times", requests.Load())
	}
}
