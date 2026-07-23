package pluginhost

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type blockingGuardPluginClient struct {
	started  chan struct{}
	release  chan struct{}
	shutdown atomic.Int32
}

func (c *blockingGuardPluginClient) Call(context.Context, string, []byte) ([]byte, error) {
	close(c.started)
	<-c.release
	return nil, nil
}

func (c *blockingGuardPluginClient) Shutdown() {
	c.shutdown.Add(1)
}

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

func TestGuardedPluginClientShutdownContextDetachesBlockedCall(t *testing.T) {
	inner := &blockingGuardPluginClient{started: make(chan struct{}), release: make(chan struct{})}
	guarded := newGuardedPluginClient(inner)

	callDone := make(chan struct{})
	go func() {
		_, _ = guarded.Call(context.Background(), "blocked", nil)
		close(callDone)
	}()
	select {
	case <-inner.started:
	case <-time.After(time.Second):
		t.Fatal("guarded call did not start")
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	cancelShutdown()
	shutdownDone := make(chan struct{})
	go func() {
		guarded.ShutdownContext(shutdownCtx)
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("context-canceled guarded shutdown waited for the active call")
	}
	if got := inner.shutdown.Load(); got != 0 {
		t.Fatalf("shutdown calls before active call exits = %d, want 0", got)
	}

	close(inner.release)
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("guarded call did not exit")
	}
	deadline := time.Now().Add(time.Second)
	for inner.shutdown.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := inner.shutdown.Load(); got != 1 {
		t.Fatalf("shutdown calls after active call exits = %d, want 1", got)
	}
}
