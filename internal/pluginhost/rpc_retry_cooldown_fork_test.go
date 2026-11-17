package pluginhost

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// Keep the stricter fork assertions separate from the upstream-owned regression.
func TestPluginErrorCarriesBoundedCooldownAndMarkerFork(t *testing.T) {
	_, errDecode := decodeEnvelopeResult[rpcEmptyResponse](pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:              "rate_limited",
			Message:           "try later",
			HTTPStatus:        http.StatusTooManyRequests,
			RetryAfterSeconds: 7,
		},
	})
	if errDecode == nil {
		t.Fatal("decodeEnvelopeResult returned nil error")
	}
	retryAfter, ok := errDecode.(interface{ RetryAfter() *time.Duration })
	if !ok || retryAfter.RetryAfter() == nil || *retryAfter.RetryAfter() != 7*time.Second {
		t.Fatalf("RetryAfter() = %#v, want 7s", retryAfter)
	}
	if got := coreauth.SafeResponseHeaders(errDecode).Get("Retry-After"); got != "7" {
		t.Fatalf("SafeResponseHeaders Retry-After = %q, want 7", got)
	}

	const (
		provider = "plugin-provider-retry-after-fork"
		model    = "test-model-retry-after-fork"
		authID   = "auth-retry-after-fork-1"
	)
	executorRecord := normalizeTestCapabilityRecord(capabilityRecord{id: "executor-plugin-retry-after-fork"})
	host := newHostWithRecords(executorRecord)
	exec := &fakeExecutor{
		identifier: provider,
		execute: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
			return pluginapi.ExecutorResponse{}, errDecode
		},
	}
	adapter := newExecutorAdapterForRecordForTest(host, executorRecord, exec,
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
	)
	adapter.provider = provider

	authMgr := coreauth.NewManager(nil, nil, nil)
	authMgr.SetRetryConfig(0, 0, 0)
	authMgr.RegisterExecutor(adapter)
	auth := &coreauth.Auth{
		ID:         authID,
		Provider:   provider,
		Status:     coreauth.StatusActive,
		FileName:   "auth-retry-after-fork-1.json",
		Attributes: map[string]string{"type": "api_key"},
	}
	if _, errReg := authMgr.Register(context.Background(), auth); errReg != nil {
		t.Fatalf("Register auth: %v", errReg)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	before := time.Now()
	_, errExec := authMgr.Execute(context.Background(), []string{provider}, coreexecutor.Request{
		Model:   model,
		Payload: []byte(`{"model":"test-model-retry-after-fork"}`),
	}, coreexecutor.Options{
		SourceFormat:   sdktranslator.FormatOpenAI,
		ResponseFormat: sdktranslator.FormatOpenAI,
	})
	after := time.Now()
	if errExec == nil {
		t.Fatal("authMgr.Execute returned nil error")
	}
	// The sole credential's 7s hint is clamped to the 10s quota cooldown floor.
	// The terminal pool error must advertise that recovery, not the raw hint.
	if got := coreauth.SafeResponseHeaders(errExec).Get("Retry-After"); got != "10" {
		t.Fatalf("SafeResponseHeaders(errExec) Retry-After = %q, want 10 (quota cooldown floor)", got)
	}
	var marker interface{ IsModelCooldown() bool }
	if !errors.As(errExec, &marker) || marker == nil || !marker.IsModelCooldown() {
		t.Fatalf("error %T must report IsModelCooldown() == true", errExec)
	}
	updated, okAuth := authMgr.GetByID(authID)
	if !okAuth || updated == nil {
		t.Fatalf("GetByID(%q) missing", authID)
	}
	state := updated.ModelStates[model]
	if state == nil || state.NextRetryAfter.Before(before.Add(10*time.Second)) || state.NextRetryAfter.After(after.Add(10*time.Second)) {
		t.Fatalf("model NextRetryAfter = %v, want within [%v, %v] (10s quota cooldown floor)", state, before.Add(10*time.Second), after.Add(10*time.Second))
	}
}
