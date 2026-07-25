//go:build windows

package pluginhost

import (
	"context"
	"fmt"
	"sync"
)

type synchronousWindowsPluginLoader struct {
	inner pluginLoader
}

func (l synchronousWindowsPluginLoader) Open(file pluginFile, host *Host) (pluginClient, error) {
	if l.inner == nil {
		return nil, fmt.Errorf("plugin loader is unavailable")
	}
	client, errOpen := l.inner.Open(file, host)
	if errOpen != nil {
		return nil, errOpen
	}
	if client == nil {
		return nil, fmt.Errorf("plugin loader returned nil client")
	}
	return newSynchronousWindowsPluginClient(client), nil
}

type synchronousWindowsPluginClient struct {
	mu    sync.Mutex
	inner pluginClient
}

func newSynchronousWindowsPluginClient(inner pluginClient) pluginClient {
	return &synchronousWindowsPluginClient{inner: inner}
}

func (*synchronousWindowsPluginClient) requiresSynchronousCall() {}

func (c *synchronousWindowsPluginClient) Call(ctx context.Context, method string, request []byte) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("plugin client is closed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return nil, fmt.Errorf("plugin client is closed")
	}
	return c.inner.Call(ctx, method, request)
}

func (c *synchronousWindowsPluginClient) Shutdown() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return
	}
	c.inner.Shutdown()
	c.inner = nil
}
