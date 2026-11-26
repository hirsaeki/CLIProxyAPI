package executor

import (
	"context"
	"errors"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestAIStudioCanceledFullBufferKeepsObservedFailureFork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := make(chan cliproxyexecutor.StreamChunk, 1)
	out <- cliproxyexecutor.StreamChunk{Payload: []byte("undelivered")}
	want := newGeminiZeroOutputStatusErr("observed terminal failure")
	var pending error = want
	if !stopAIStudioTranslatedStreamFork(ctx, &pending, nil, nil, out) || pending != nil {
		t.Fatal("observed failure was not consumed")
	}
	select {
	case chunk := <-out:
		if !errors.Is(chunk.Err, want) || len(chunk.Payload) != 0 {
			t.Fatalf("terminal chunk = %+v, want the observed failure without canceled payload", chunk)
		}
	default:
		t.Fatal("observed failure did not reach the conductor channel")
	}
}
