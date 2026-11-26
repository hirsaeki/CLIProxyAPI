package auth

import (
	"context"
	"errors"
	"net/http"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// A typed, availability-neutral upstream 502 can be known to the producer
// before the caller cancels on its translated frame. Do not treat a transport
// error caused by the caller, or a credential cooldown hint, as that outcome.
func retainObservedStreamFailureFork(err error) bool {
	return statusCodeFromError(err) == http.StatusBadGateway &&
		errorCodeFromError(err) == transientTransportErrorCode &&
		!isCredentialScopedError(err) && retryAfterFromError(err) == nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// Watch only the first terminal error, without delaying closure of the caller's
// stream. As with the ordinary drain, keep reading until the producer stops.
func drainObservedStreamFailureFork(ch <-chan cliproxyexecutor.StreamChunk, record func(error)) {
	if ch == nil {
		return
	}
	go func() {
		for chunk := range ch {
			if chunk.Err == nil {
				continue
			}
			if retainObservedStreamFailureFork(chunk.Err) {
				record(chunk.Err)
			}
			for range ch {
			}
			return
		}
	}()
}
