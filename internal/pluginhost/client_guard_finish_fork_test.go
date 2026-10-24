package pluginhost

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

type finishSynchronousClientFork struct {
	call func(context.Context) ([]byte, error)
}

func (*finishSynchronousClientFork) requiresSynchronousCall() {}
func (*finishSynchronousClientFork) Shutdown()                {}
func (c *finishSynchronousClientFork) Call(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	return c.call(ctx)
}

// Keep a recognizable frame to detect an accidental move to a worker goroutine.
func invokeSynchronousGuardFork(c *guardedPluginClient, ctx context.Context) ([]byte, error) {
	return c.Call(ctx, "auth.authenticate", nil)
}

func TestGuardedSynchronousCallFinishFork(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "canceled", "cancel-during-call"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			var finishes atomic.Int32
			var active atomic.Bool
			var earlyFinish atomic.Bool
			ctx, fallback := withPluginCallFinish(ctx, func() {
				if active.Load() {
					earlyFinish.Store(true)
				}
				finishes.Add(1)
			})
			wantErr := errors.New("native call error")
			inner := &finishSynchronousClientFork{call: func(callCtx context.Context) ([]byte, error) {
				active.Store(true)
				defer active.Store(false)
				pcs := make([]uintptr, 32)
				frames := runtime.CallersFrames(pcs[:runtime.Callers(0, pcs)])
				callerPresent := false
				for {
					frame, more := frames.Next()
					if strings.HasSuffix(frame.Function, ".invokeSynchronousGuardFork") {
						callerPresent = true
					}
					if !more {
						break
					}
				}
				if !callerPresent {
					t.Error("native call moved off the caller's goroutine")
				}
				if mode == "cancel-during-call" {
					cancel()
				}
				// The adapter fallback must not release a slot claimed by the guard,
				// even if cancellation occurs before the native call returns.
				fallback()
				if got := finishes.Load(); got != 0 {
					t.Errorf("finish calls while native call is active = %d, want 0", got)
				}
				switch mode {
				case "panic":
					panic("native panic")
				case "error":
					return nil, wantErr
				case "canceled", "cancel-during-call":
					return nil, callCtx.Err()
				default:
					return []byte("response"), nil
				}
			}}
			guarded := newGuardedPluginClient(inner)
			var response []byte
			var errCall error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				response, errCall = invokeSynchronousGuardFork(guarded, ctx)
			}()
			if got := finishes.Load(); got != 1 {
				t.Fatalf("finish calls after native completion = %d, want 1", got)
			}
			fallback()
			fallback()
			if got := finishes.Load(); got != 1 || earlyFinish.Load() {
				t.Fatalf("finish lifecycle: calls=%d, early=%v", got, earlyFinish.Load())
			}
			if guarded.calls != 0 {
				t.Fatalf("active guarded calls = %d, want 0", guarded.calls)
			}
			switch mode {
			case "panic":
				if recovered != "native panic" {
					t.Fatalf("panic = %v", recovered)
				}
			case "error":
				if !errors.Is(errCall, wantErr) {
					t.Fatalf("error = %v, want %v", errCall, wantErr)
				}
			case "canceled", "cancel-during-call":
				if !errors.Is(errCall, context.Canceled) {
					t.Fatalf("error = %v, want context.Canceled", errCall)
				}
			default:
				if errCall != nil || string(response) != "response" {
					t.Fatalf("response = %q, error = %v", response, errCall)
				}
			}
			if mode != "panic" && recovered != nil {
				t.Fatalf("unexpected panic = %v", recovered)
			}
		})
	}
}

func TestGuardedSynchronousClosedCallFinishFork(t *testing.T) {
	var finishes atomic.Int32
	ctx, fallback := withPluginCallFinish(context.Background(), func() { finishes.Add(1) })
	guarded := newGuardedPluginClient(&finishSynchronousClientFork{call: func(context.Context) ([]byte, error) {
		t.Fatal("closed client called")
		return nil, nil
	}})
	guarded.Shutdown()
	if _, errCall := guarded.Call(ctx, "auth.authenticate", nil); errCall == nil {
		t.Fatal("closed client did not fail")
	}
	fallback()
	fallback()
	if got := finishes.Load(); got != 1 {
		t.Fatalf("unclaimed fallback calls = %d, want 1", got)
	}
}
