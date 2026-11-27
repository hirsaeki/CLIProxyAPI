package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type closedStreamResultHookFork struct {
	NoopHook
	results chan Result
}

func (h closedStreamResultHookFork) OnResult(_ context.Context, result Result) {
	h.results <- result
}

func TestClosedStreamRetainsObservedTransientFailureFork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hook := closedStreamResultHookFork{results: make(chan Result, 2)}
	manager := NewManager(nil, nil, hook)
	auth, errRegister := manager.Register(t.Context(), &Auth{ID: "closed-stream", Provider: "aistudio"})
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	upstream := make(chan cliproxyexecutor.StreamChunk, 1)
	defer close(upstream)
	res := manager.wrapStreamResult(ctx, auth, auth.Provider, "model", "model", nil,
		[]cliproxyexecutor.StreamChunk{{Payload: []byte("event: error\n\n")}}, upstream,
		OAuthModelAliasResult{}, false, cliproxyexecutor.Options{}, 0)
	select {
	case chunk := <-res.Chunks:
		if string(chunk.Payload) != "event: error\n\n" {
			t.Fatalf("translated error frame = %q", chunk.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("translated error frame not forwarded")
	}
	cancel()
	select {
	case _, open := <-res.Chunks:
		if open {
			t.Fatal("unexpected extra caller chunk")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation must close the caller's stream without waiting for upstream")
	}
	// Queue the typed failure after the consumer is gone, forcing the
	// interleaving that previously lost the conductor result.
	upstream <- cliproxyexecutor.StreamChunk{Err: &Error{
		HTTPStatus: http.StatusBadGateway, Code: transientTransportErrorCode, Message: "upstream zero output",
	}}
	select {
	case got := <-hook.results:
		if got.Success || got.Error == nil || got.Error.Code != transientTransportErrorCode {
			t.Fatalf("unexpected observed result: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("conductor dropped the confirmed upstream failure")
	}
	current, ok := manager.GetByID(auth.ID)
	if !ok || current.Success != 0 || current.Failed != 1 || current.Unavailable || !current.NextRetryAfter.IsZero() {
		t.Fatalf("confirmed failure unexpectedly changed credential availability: %+v", current)
	}
}
