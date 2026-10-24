package home

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type ownerDialCountedConnFork struct {
	net.Conn
	closes atomic.Int32
}

func (c *ownerDialCountedConnFork) Close() error { c.closes.Add(1); return c.Conn.Close() }

func TestOwnerBoundDialerLateSuccessFork(t *testing.T) {
	for _, mode := range []string{"owner", "caller"} {
		t.Run(mode, func(t *testing.T) {
			owner, cancelOwner := context.WithCancel(context.Background())
			defer cancelOwner()
			caller, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			conn := &ownerDialCountedConnFork{Conn: local}
			entered, release := make(chan struct{}), make(chan struct{})
			dial := ownerBoundDialerFork(owner, func(context.Context, string, string) (net.Conn, error) { close(entered); <-release; return conn, nil })
			done := make(chan error, 1)
			go func() {
				got, err := dial(caller, "tcp", "unused")
				if got != nil {
					done <- errors.New("late connection published")
					return
				}
				done <- err
			}()
			<-entered
			if mode == "owner" {
				cancelOwner()
			} else {
				cancelCaller()
			}
			close(release)
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("dial did not finish")
			}
			if got := conn.closes.Load(); got != 1 {
				t.Fatalf("late connection closes = %d", got)
			}
		})
	}
}

func TestOwnerBoundDialerDetachedCancellationFork(t *testing.T) {
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	dial := ownerBoundDialerFork(owner, func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go func() { _, err := dial(context.Background(), "tcp", "unused"); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("detached dial ignored owner cancellation")
	}
}

func TestOwnerBoundDialerPreCancelledFork(t *testing.T) {
	for _, mode := range []string{"owner", "caller"} {
		t.Run(mode, func(t *testing.T) {
			owner, cancelOwner := context.WithCancel(context.Background())
			defer cancelOwner()
			caller, cancelCaller := context.WithCancel(context.Background())
			defer cancelCaller()
			if mode == "owner" {
				cancelOwner()
			} else {
				cancelCaller()
			}
			dial := ownerBoundDialerFork(owner, func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("cancelled context reached dialer")
				return nil, nil
			})
			if _, err := dial(caller, "tcp", "unused"); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOwnerBoundDialerPreservesContextFork(t *testing.T) {
	type key struct{}
	deadline := time.Now().Add(time.Minute)
	caller, cancel := context.WithDeadline(context.WithValue(context.Background(), key{}, "value"), deadline)
	defer cancel()
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	var dialContext context.Context
	dial := ownerBoundDialerFork(context.Background(), func(ctx context.Context, _, _ string) (net.Conn, error) {
		dialContext = ctx
		if value := ctx.Value(key{}); value != "value" {
			t.Fatalf("value = %v", value)
		}
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			t.Fatalf("deadline = %v, %v", got, ok)
		}
		return local, nil
	})
	got, err := dial(caller, "tcp", "unused")
	if err != nil || got != local {
		t.Fatalf("dial = %v, %v", got, err)
	}
	if !errors.Is(dialContext.Err(), context.Canceled) {
		t.Fatal("temporary dial context not released")
	}
	// Releasing the temporary context must not cancel an established socket.
	received := make(chan error, 1)
	go func() { _, err := peer.Write([]byte{1}); received <- err }()
	b := make([]byte, 1)
	if _, err := got.Read(b); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
}
