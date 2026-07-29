package pluginhost

import (
	"context"
	"testing"
	"time"
)

type synchronousBlockingGuardPluginClient struct {
	blockingGuardPluginClient
}

func (*synchronousBlockingGuardPluginClient) requiresSynchronousCall() {}

func TestGuardedPluginClientPreservesSynchronousCall(t *testing.T) {
	inner := &synchronousBlockingGuardPluginClient{blockingGuardPluginClient: blockingGuardPluginClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}}
	guarded := newGuardedPluginClient(inner)

	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, errCall := guarded.Call(ctx, "blocked", nil)
		callDone <- errCall
	}()
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("guarded call did not start")
	}

	cancel()
	select {
	case errCall := <-callDone:
		close(inner.release)
		t.Fatalf("synchronous guarded call returned before the inner call: %v", errCall)
	case <-time.After(50 * time.Millisecond):
	}

	close(inner.release)
	select {
	case errCall := <-callDone:
		if errCall != nil {
			t.Fatalf("Call() error = %v", errCall)
		}
	case <-time.After(time.Second):
		t.Fatal("synchronous guarded call did not exit")
	}
}
