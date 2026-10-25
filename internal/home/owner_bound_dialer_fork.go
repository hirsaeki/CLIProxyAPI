package home

import (
	"context"
	"net"
)

// ownerBoundDialerFork binds detached Redis pool dials to the command lifetime.
// Keep the pool context's values and deadline, and use a context-aware TLS dialer.
func ownerBoundDialerFork(owner context.Context, dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := owner.Err(); err != nil {
			return nil, context.Cause(owner)
		}
		dialCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(owner, cancel)
		defer func() { stop(); cancel() }()
		if owner.Err() != nil {
			cancel()
		}
		conn, err := dial(dialCtx, network, address)
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err == nil {
			err = context.Cause(owner)
		}
		if err != nil {
			_ = closeUnderlyingTransport(conn)
			return nil, err
		}
		return conn, nil
	}
}
