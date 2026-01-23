# Upstream Merge & Thinking Recovery Reintegration

**Status**: ✅ Completed  
**Date**: 2026-01-24

## Overview

upstreamとマージ後、削除されたThinking Error Recovery機能をコンフリクトが起きにくい形で再統合した。

## Background

- upstreamの`upstream/main`をマージした際、`antigravity_executor.go`でコンフリクト発生
- upstreamはThinking Error Recoveryのコード（リトライループ、フック呼び出し）を削除
- フォークで実装した`antigravity_thinking_helpers.go`のヘルパー関数は残存

## Solution: Wrapper Pattern

upstreamのコード内部を変更するのではなく、**ラッパー関数**で既存のメソッドを包む形で機能を再統合。

### New File: `antigravity_thinking_recovery.go`

```go
// executeClaudeNonStreamWithRecovery wraps executeClaudeNonStream
func (e *AntigravityExecutor) executeClaudeNonStreamWithRecovery(...) {
    // Pre-sanitize payload (remove known invalid signatures)
    // Call original executeClaudeNonStream
    // On error, check if thinking signature error → retry with sanitized payload
}

// executeClaudeStreamWithRecovery wraps executeStreamInternal
func (e *AntigravityExecutor) executeClaudeStreamWithRecovery(...) {
    // Same pattern as above
}
```

### Changes to `antigravity_executor.go`

1. **Execute()** - 呼び出し先を変更（1行）:
   ```go
   // Before
   return e.executeClaudeNonStream(ctx, auth, req, opts)
   // After  
   return e.executeClaudeNonStreamWithRecovery(ctx, auth, req, opts)
   ```

2. **ExecuteStream()** - Claude分岐追加 + 内部関数抽出:
   ```go
   // Added Claude model branch
   if isClaude || strings.Contains(baseModel, "gemini-3-pro") {
       return e.executeClaudeStreamWithRecovery(ctx, auth, req, opts)
   }
   return e.executeStreamInternal(ctx, auth, req, opts)
   
   // Extracted existing logic to executeStreamInternal
   func (e *AntigravityExecutor) executeStreamInternal(...) { ... }
   ```

## Conflict Resistance

| 変更箇所 | コンフリクト耐性 |
|----------|------------------|
| `antigravity_thinking_recovery.go` | 新規ファイル → コンフリクトなし |
| `Execute()` 呼び出し行 | 1行のみ → 低リスク |
| `ExecuteStream()` 先頭分岐 | 関数先頭 → upstreamの内部変更と干渉しにくい |
| `executeStreamInternal` 抽出 | 関数追加 → コンフリクトなし |

## File Structure

```
internal/runtime/executor/
├── antigravity_executor.go           # Upstream code + minimal hooks
├── antigravity_thinking_helpers.go   # Helper functions (unchanged)
├── antigravity_thinking_helpers_test.go
└── antigravity_thinking_recovery.go  # NEW: Wrapper functions (fork enhancement)
```

## Testing

```
go test ./internal/runtime/executor/... -run Antigravity -v
# All tests pass
```

## Related Documents

- [antigravity-thinking-error-recovery.md](./antigravity-thinking-error-recovery.md) - Original implementation spec
- [thinking-error-handling.md](./thinking-error-handling.md) - Claude executor implementation
