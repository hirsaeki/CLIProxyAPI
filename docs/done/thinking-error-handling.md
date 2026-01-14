# Claude Thinking Error Handling (Low-Conflict)

**Release:** `v6.6.104-thinking-1`

## Overview

Automatic recovery and session-based optimization for Claude 3.7+ "Invalid signature in thinking block" errors with low-conflict implementation for fork maintenance.

## Changes

### Cache Layer

- **`internal/cache/invalid_signature_cache.go`**: Thread-safe blacklist for invalid signatures with a 3-hour TTL.

### Executor Layer

- **`internal/runtime/executor/claude_thinking_helpers.go`**:
  - `deriveSessionIDFromClaudeMessages`
  - `isClaudeThinkingInvalidSignatureError`
  - `extractInvalidThinkingPath`
  - `extractErrorMessage` (helper)
  - `replaceThinkingBlocks` (helper)
  - `sanitizeClaudePayloadForInvalidThinking`
  - `stripClaudeThinkingBlocksForRetry`
  - `recordInvalidThinkingSignatureFromError`
  - `handleThinkingErrorRecovery` (helper)
- **`internal/runtime/executor/claude_executor.go`**:
  - Integrated retry logic in `Execute` and `ExecuteStream`.
  - Minimal hooks added to sanitization and handle 400 errors.

## How It Works

- **Robust Error Recovery**: Problematic thinking blocks are blacklisted and replaced with placeholders upon 400 errors.
- **Session-Based Optimization**: Subsequent requests in the same session pre-emptively strip invalid signatures to avoid repeated API failures.
- **Minimal Conflict Hook**: Added to `claude_executor.go` as a self-contained block:

  ```go
  // --- Start Thinking Error Recovery Hook ---
  sessionID := deriveSessionIDFromClaudeMessages(bodyForTranslation)
  bodyForUpstream, _ = sanitizeClaudePayloadForInvalidThinking(bodyForUpstream, sessionID)
  // --- End Thinking Error Recovery Hook ---
  ```

## Tests

- `TestInvalidSignatureCache`
- `TestIsClaudeThinkingInvalidSignatureError`
- `TestExtractInvalidThinkingPath`
- `TestSanitizeClaudePayloadForInvalidThinking`
- `TestStripClaudeThinkingBlocksForRetry`

## Commits

| Hash | Message |
|------|---------|
| `41bb9ce` | feat(executor): add Claude thinking error recovery with retry logic |
| `a0dade3` | refactor(executor): simplify thinking error recovery code |

## Related Documents

- [thinking_error_handling_plan.md](./thinking_error_handling_plan.md) - Original planning document
