package home

import (
	"context"
	"net"
	"sync"
)

// rebalanceTransport owns only the sockets of one dedicated rebalance client.
// Closing them before Redis pool shutdown avoids its health probe waiting behind
// a blocked socket read. Dial completion racing shutdown must not leak a socket.
type rebalanceTransport struct {
	ctx       context.Context
	cancel    context.CancelFunc
	dial      func(context.Context, string, string) (net.Conn, error)
	mu        sync.Mutex
	closed    bool
	conns     []net.Conn
	closeOnce sync.Once
}

func newRebalanceTransport(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) *rebalanceTransport {
	ctx, cancel := context.WithCancel(ctx)
	return &rebalanceTransport{ctx: ctx, cancel: cancel, dial: dial}
}

func (t *rebalanceTransport) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	// Redis can detach its pool dial context from the command's cancellation.
	// Keep that context's values/deadline while binding it to our owner lifetime.
	dialCtx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(t.ctx, cancel)
	if t.ctx.Err() != nil {
		cancel()
	}
	conn, err := t.dial(dialCtx, network, address)
	stopCancel()
	cancel()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	err = ctx.Err()
	if err == nil && t.closed {
		err = net.ErrClosed
	}
	if err == nil {
		err = t.ctx.Err()
	}
	if err == nil {
		t.conns = append(t.conns, conn)
	}
	t.mu.Unlock()
	if err != nil {
		_ = closeUnderlyingTransport(conn)
		return nil, err
	}
	return conn, nil
}

func (t *rebalanceTransport) Close() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		conns := t.conns
		t.conns = nil
		t.mu.Unlock()
		t.cancel()
		for _, conn := range conns {
			_ = closeUnderlyingTransport(conn)
		}
	})
}
