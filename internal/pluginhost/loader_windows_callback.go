//go:build windows

package pluginhost

import (
	"context"
	"fmt"
)

type windowsHostCallResult struct {
	payload []byte
	err     error
}

// callHostFromWindowsCallback keeps blocking host work off the foreign-thread
// callback stack, where it can disrupt the enclosing Go c-shared DLL call.
func callHostFromWindowsCallback(entry dynamicHostCallbackEntry, ctx context.Context, method string, request []byte) ([]byte, error) {
	resultCh := make(chan windowsHostCallResult, 1)
	go func() {
		resultCh <- invokeHostFromWindowsCallback(entry, ctx, method, request)
	}()
	result := <-resultCh
	return result.payload, result.err
}

func invokeHostFromWindowsCallback(entry dynamicHostCallbackEntry, ctx context.Context, method string, request []byte) (result windowsHostCallResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = windowsHostCallResult{err: fmt.Errorf("host callback panic: %v", recovered)}
		}
	}()
	result.payload, result.err = entry.host.callFromPlugin(ctx, method, request)
	return result
}
