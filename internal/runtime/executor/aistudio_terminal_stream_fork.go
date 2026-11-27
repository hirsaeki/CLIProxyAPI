package executor

import (
	"context"
	"errors"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// A translated zero-output error can be known before its typed result reaches
// the conductor. If cancellation leaves an undelivered payload in the single
// producer's buffered channel, replace that payload with the known result.
func stopAIStudioTranslatedStreamFork(ctx context.Context, retainedErr *error, reporter *helps.UsageReporter, streamUsage *helps.StreamUsageBuffer, out chan cliproxyexecutor.StreamChunk) bool {
	var terminal statusErr
	if retainedErr == nil || !errors.As(*retainedErr, &terminal) ||
		terminal.code != http.StatusBadGateway || !terminal.transientTransport || cap(out) == 0 {
		return helps.StopTranslatedClaudeStream(ctx, retainedErr, reporter, streamUsage, out)
	}
	err := *retainedErr
	*retainedErr = nil
	helps.PublishTranslatedClaudeStreamFailure(ctx, err, reporter, streamUsage)
	chunk := cliproxyexecutor.StreamChunk{Err: err}
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
	}
	select {
	case out <- chunk:
		return true
	default:
	}
	// Only this producer sends to out. A competing reader can only make
	// additional capacity available once the caller has canceled.
	select {
	case <-out:
	default:
	}
	out <- chunk
	return true
}
