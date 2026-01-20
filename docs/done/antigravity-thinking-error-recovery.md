# Antigravity Thinking Error Recovery

**Status**: ✅ Completed

## Overview

AntigravityExecutorにClaudeExecutorと同様のthinking署名エラーリカバリを実装する。

## Background

- ClaudeExecutor（直接Anthropic API）には `claude_thinking_helpers.go` でエラーリカバリが実装済み
- AntigravityExecutor（Google経由）には未実装
- 実際にAntigravityで署名エラー（400）が発生している

エラー例:
```json
{
  "error": {
    "code": 400,
    "message": "{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"messages.1.content.0: Invalid `signature` in `thinking` block\"},\"request_id\":\"...\"}",
    "status": "INVALID_ARGUMENT"
  }
}
```

## Implementation Plan

### 1. New File: `antigravity_thinking_helpers.go`

**Location**: `internal/runtime/executor/antigravity_thinking_helpers.go`

```go
// エラー検出 - Google RPC内のAnthropicエラーをパース
func isAntigravityThinkingSignatureError(statusCode int, body []byte) bool

// パス抽出 - "messages.X.content.Y" から位置を取得  
func extractAntigravityInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool)

// 無効署名を記録（既存のinvalid_signature_cacheを再利用）
func recordAntigravityInvalidSignature(sessionID string, originalPayload, errBody []byte) (string, bool)

// ブラックリスト署名を事前除去
func sanitizeAntigravityPayloadForInvalidThinking(originalPayload []byte, sessionID string) ([]byte, bool)

// 全thinking削除フォールバック
func stripAntigravityThinkingBlocksForRetry(originalPayload []byte) ([]byte, bool)

// リカバリ統合関数
func handleAntigravityThinkingErrorRecovery(sessionID string, originalPayload, errBody []byte, statusCode int) ([]byte, bool)
```

**Key Points**:
- エラーメッセージが2段階（Google RPC → 内部Anthropic JSON）なのでパースを調整
- 元のClaude形式ペイロード（`originalPayload`）から署名を特定
- 既存の `invalid_signature_cache.go` を再利用

### 2. Modify: `antigravity_executor.go`

**Changes**:
- `executeClaudeNonStream` にフック追加
- `executeClaudeStream` にフック追加

```go
// --- Start Thinking Error Recovery Hook ---
sessionID := deriveSessionIDFromClaudeMessages(originalPayload)
originalPayload, _ = sanitizeAntigravityPayloadForInvalidThinking(originalPayload, sessionID)
// 再変換が必要
translatedPayload = retranslate(originalPayload)
// --- End Thinking Error Recovery Hook ---

// リトライループ (max 1 retry)
for attempt := 0; attempt < 2; attempt++ {
    // ... 既存の送信処理 ...
    
    if statusCode == 400 {
        if newPayload, shouldRetry := handleAntigravityThinkingErrorRecovery(
            sessionID, originalPayload, responseBody, statusCode); shouldRetry {
            log.Infof("Antigravity thinking signature error. Retrying.")
            originalPayload = newPayload
            translatedPayload = retranslate(newPayload)
            continue
        }
    }
    return error
}
```

### 3. Retranslation

リトライ時に元ペイロードを修正した後、再度Antigravity形式への変換が必要:

```go
translatedPayload, _ = antigravityclaude.ConvertClaudeRequestToAntigravity(
    newOriginalPayload, sessionID, modelName)
```

## File Changes

| File | Change |
|------|--------|
| `internal/runtime/executor/antigravity_thinking_helpers.go` | **New** |
| `internal/runtime/executor/antigravity_thinking_helpers_test.go` | **New** |
| `internal/runtime/executor/antigravity_executor.go` | Add hooks (2 locations) |

## Reused Components

| Component | Usage |
|-----------|-------|
| `invalid_signature_cache.go` | Reuse as-is (session ID + signature blacklist) |
| `deriveSessionIDFromClaudeMessages` | Reuse as-is (derive from Claude format) |
| `thinkingPlaceholder` | Use same placeholder |

## Tests

- `TestIsAntigravityThinkingSignatureError` - Google RPC error detection
- `TestExtractAntigravityInvalidThinkingPath` - Path extraction
- `TestSanitizeAntigravityPayloadForInvalidThinking` - Blacklist signature removal
- `TestHandleAntigravityThinkingErrorRecovery` - Integration test

## Estimate

| Task | Time |
|------|------|
| antigravity_thinking_helpers.go | 30min |
| antigravity_executor.go changes | 30min |
| Tests | 30min |
| Verification | 15min |
| **Total** | **~1.5h** |

## Implementation Summary

### Files Created
- `internal/runtime/executor/antigravity_thinking_helpers.go` - Helper functions for error detection and recovery
- `internal/runtime/executor/antigravity_thinking_helpers_test.go` - Unit tests

### Files Modified
- `internal/runtime/executor/antigravity_executor.go` - Added hooks in `executeClaudeNonStream` and `ExecuteStream`

### Key Functions
- `isAntigravityThinkingSignatureError` - Detects thinking signature errors in Google RPC envelope
- `extractAntigravityErrorMessage` - Parses nested JSON error messages
- `handleAntigravityThinkingErrorRecovery` - Orchestrates error recovery with retry

### Test Results
All tests pass:
```
=== RUN   TestIsAntigravityThinkingSignatureError
=== RUN   TestExtractAntigravityInvalidThinkingPath
=== RUN   TestSanitizeAntigravityPayloadForInvalidThinking
=== RUN   TestStripAntigravityThinkingBlocksForRetry
=== RUN   TestHandleAntigravityThinkingErrorRecovery
PASS
```

## Related Documents

- [docs/done/thinking-error-handling.md](../done/thinking-error-handling.md) - Original ClaudeExecutor implementation
- [docs/think.md](../think.md) - Extended thinking specifications
