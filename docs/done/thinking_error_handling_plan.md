# Anthropic Thinking Block 'Invalid signature' Error Handling Implementation Plan

## 現状の課題

Anthropic API（Claude 3.7 Sonnet 等）において、`thinking` ブロック（思考プロセス）を含むメッセージの署名（`signature`）が不一致になると、以下のエラーが発生してリクエストが失敗する。

> `messages.1.content.0: Invalid signature in thinking block`

これは、クライアント側（SillyTavern等）で保存されている `thinking` ブロックとその `signature` が、プロキシを介した際や履歴の編集によって Anthropic 側の期待値とズレることで発生する。

## 解決策：自動リトライとブロック除去（プレースホルダ化）

このエラーが発生した際、プロキシ側で自動的に `thinking` ブロックを通常の `text` ブロックへ変換してリトライすることで、署名検証を回避しつつ会話を継続させる。

### 主なロジック

1. **エラー検知**:
    - Upstream からの `400 Bad Request` を受信した際、エラーボディの JSON（`error.message` / `message`）を優先して検査し、フォールバックとしてボディ文字列を検査する。
    - `invalid signature` と `thinking` を含む場合に「thinking 署名エラー」とみなす。
2. **メッセージの正規化（安全優先）**:
    - `messages` 配列内の `content` を走査。
    - `type: "thinking"` / `type: "redacted_thinking"`（および `thinking`+`signature` を持つ thinking 互換の形）を `type: "text"` に置換する。
    - **思考内容は再送しない**（プロンプト汚染・肥大化を避ける）。置換テキストは固定プレースホルダ（例: `[Previous thinking omitted due to invalid signature]`）とする。
    - `signature` は結果的に消える（置換のため）。
3. **単発リトライ**:
    - 変換後のペイロードで **最大 1 回のみ** 再送する（無限ループ防止）。

## 懸念点と影響範囲

- **不整合**: プロキシと Anthropic 間では `thinking` ブロックが消えるが、クライアント側には残る。
- **モデルの振る舞い**: `thinking` が失われるため推論への影響はありうるが、エラーで停止するより会話継続を優先する。
- **安全性**: `thinking` 内容をそのまま `text` として再投入すると、過去の思考が外部入力扱いになりやすく、注入・誤誘導・トークン消費につながるため、デフォルトはプレースホルダ化とする。

## 追加改善案：セッション中の恒久対応（無効署名の局所ストリップ）

単発リトライ方式だけだと、クライアントが毎回同じ履歴（壊れた `signature` を含む `thinking`）を再送する場合、以後も毎回「1回目 400 → 2回目成功」になりがちで、遅延・コスト・レート制限リスクが増える。

そこで「どの `thinking.signature` が壊れているか」を特定できた場合、**同一セッション中（TTL=3時間）に限り、その `signature` を持つ thinking だけを事前にプレースホルダ化**して送ることで、以後のリクエストを 1 回で通す。

### 方式概要

1. Upstream から `Invalid signature in thinking block` が返ったら、エラーメッセージから `messages.<i>.content.<j>` を抽出する（例: `messages.1.content.0`）。
2. 送信したリクエストボディから `messages[i].content[j].signature` を取り出し、「無効署名」としてセッションキャッシュに保存（TTL=3時間）。
3. 次回以降のリクエスト送信前に、`messages[].content[]` を走査し、セッションキャッシュに載っている `signature` を持つ thinking ブロックだけをプレースホルダ `text` に置換して送る。

### セッション識別（提案）

クライアントから明示的なセッションIDが来ない前提で、**リクエスト内容から安定した sessionID を導出**する。
最小構成は「最初の user メッセージのテキストをハッシュ化」。

参考コード（既存実装の流用イメージ）:

```go
// deriveSessionIDFromClaudeMessages generates a stable session ID from the request payload.
// Uses the hash of the first user message text to identify the conversation.
func deriveSessionIDFromClaudeMessages(body []byte) string {
  messages := gjson.GetBytes(body, "messages")
  if !messages.IsArray() {
    return ""
  }
  for _, msg := range messages.Array() {
    if msg.Get("role").String() != "user" {
      continue
    }
    // Prefer string content, else first text part.
    content := msg.Get("content").String()
    if content == "" {
      content = msg.Get("content.0.text").String()
    }
    if content == "" {
      continue
    }
    sum := sha256.Sum256([]byte(content))
    return hex.EncodeToString(sum[:16]) // 128-bit
  }
  return ""
}
```

### 無効署名キャッシュ（TTL=3時間）

`signature` 自体をキーにして「このセッションでは無効」を覚える（3時間後に自然消滅）。

参考コード（新規キャッシュ案）:

```go
// package cache
const InvalidSignatureCacheTTL = 3 * time.Hour

func CacheInvalidSignature(sessionID, signature string) {}
func IsInvalidSignature(sessionID, signature string) bool { return false }
```

### エラーメッセージからの位置抽出

Anthropic のエラーは `error.message` に `messages.<i>.content.<j>:` のようなパスが含まれることが多い。

参考コード:

```go
var re = regexp.MustCompile(`messages\\.(\\d+)\\.content\\.(\\d+)`)

func extractInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool) {
  msg := gjson.GetBytes(errBody, "error.message").String()
  if msg == "" {
    msg = gjson.GetBytes(errBody, "message").String()
  }
  if msg == "" {
    msg = string(errBody)
  }
  m := re.FindStringSubmatch(msg)
  if len(m) != 3 {
    return 0, 0, false
  }
  mi, _ := strconv.Atoi(m[1])
  pi, _ := strconv.Atoi(m[2])
  return mi, pi, true
}
```

### 送信前サニタイズ（局所ストリップ）

参考コード:

```go
func stripInvalidThinkingSignatures(payload []byte, sessionID string) (out []byte, changed bool) {
  // Walk messages[].content[]; if part is thinking-like and signature is blacklisted,
  // replace with {type:"text", text:"[Previous thinking omitted due to invalid signature]"}.
  return payload, false
}
```

### 実行フロー（Claude Executor）

- 送信前: `sessionID := deriveSessionIDFromClaudeMessages(bodyForTranslation)` を計算し、`stripInvalidThinkingSignatures` を適用してから送る（キャッシュに何も無ければ no-op）。
- エラー時: `extractInvalidThinkingPath` が取れれば、該当パーツの `signature` を `CacheInvalidSignature` に保存し、再度 `stripInvalidThinkingSignatures` をかけてリトライ。
- 位置抽出や `signature` 特定に失敗した場合は、従来通りの「全 thinking プレースホルダ化」単発リトライにフォールバック。

## 実装計画

Upstream（本家リポジトリ）との同期（upstream sync）を容易にするため、**「ヘルパーファイルへの集約」と「最小限のフック」**戦略を採用する。これにより、`internal/runtime/executor/claude_executor.go` の変更を「数行のフック + 既存ログ/計測の維持」に抑え、競合を最小化する。

### 方針（要点）

- **通常時（送信前）**: セッションキャッシュに登録済みの「無効 signature」を持つ thinking だけ局所的にプレースホルダ化して送る（no-op が基本）。
- **エラー時（400 invalid signature）**: エラーメッセージの `messages.<i>.content.<j>` から該当 `signature` を特定してキャッシュに登録し、同一リクエストを **最大 1 回** リトライする。
- **フォールバック**: 位置抽出や `signature` 特定に失敗した場合は「全 thinking プレースホルダ化」で単発リトライする。
- **TTL**: 無効 signature キャッシュは **3時間**（それ以上はクライアント側でセッションをハンドオフ/新規会話にする前提）。

### [Claude Executor]

#### [MODIFY] `internal/runtime/executor/claude_executor.go`（最小フック）

- 送信直前に `sanitizeClaudePayloadForInvalidThinking(...)` を呼び出し、セッションキャッシュに基づく局所ストリップを適用する。
- Upstream から非 2xx 応答を受けたら、`isClaudeThinkingInvalidSignatureError(...)` で判定し、該当すれば `recordInvalidThinkingSignatureFromError(...)` でキャッシュ登録してから **最大 1 回** リトライする。
- ストリーミング (`ExecuteStream`) は、最初の HTTP 応答が非 2xx の場合のみボディを読み切ってクローズし、リトライ判定を行う（SSE が開始してからのエラーには介入しない）。
- OAuth トークン時の `applyClaudeToolPrefix` を考慮し、リトライ前後で同様に適用する（送信ボディと翻訳用ボディの整合性維持）。

#### [NEW] `internal/runtime/executor/claude_thinking_helpers.go`（低コンフリクト用ヘルパー集約）

複雑なロジックはこの新規ファイルに集約し、`claude_executor.go` は薄いフックに留める。

- `deriveSessionIDFromClaudeMessages(body []byte) string`
- `isClaudeThinkingInvalidSignatureError(statusCode int, body []byte) bool`
- `extractInvalidThinkingPath(errBody []byte) (msgIndex, partIndex int, ok bool)`
- `recordInvalidThinkingSignatureFromError(sessionID string, sentPayload []byte, errBody []byte) (signature string, ok bool)`
- `sanitizeClaudePayloadForInvalidThinking(payload []byte, sessionID string) (out []byte, changed bool)`（局所ストリップ）
- `stripClaudeThinkingBlocksForRetry(payload []byte) (out []byte, changed bool)`（全 thinking プレースホルダ化のフォールバック）

参考コード（セッションID導出。既存の `internal/translator/antigravity/claude` の発想を流用）:

```go
func deriveSessionIDFromClaudeMessages(body []byte) string {
  messages := gjson.GetBytes(body, "messages")
  if !messages.IsArray() {
    return ""
  }
  for _, msg := range messages.Array() {
    if msg.Get("role").String() != "user" {
      continue
    }
    content := msg.Get("content").String()
    if content == "" {
      content = msg.Get("content.0.text").String()
    }
    if content == "" {
      continue
    }
    sum := sha256.Sum256([]byte(content))
    return hex.EncodeToString(sum[:16]) // 128-bit
  }
  return ""
}
```

参考コード（エラーメッセージから位置抽出。Goの生文字列前提）:

```go
var re = regexp.MustCompile(`messages\.(\d+)\.content\.(\d+)`)
```

#### [ADD] `internal/cache/invalid_signature_cache.go`（TTL=3時間）

- セッションID単位で「無効 signature」を記録・参照するキャッシュ（TTL=3時間）。
- API例:

```go
func CacheInvalidSignature(sessionID, signature string) {}
func IsInvalidSignature(sessionID, signature string) bool { return false }
```

## 検証プラン

- 単体テスト: `isClaudeThinkingInvalidSignatureError` の判定（JSON優先＋フォールバック、400限定）。
- 単体テスト: `extractInvalidThinkingPath` が `messages.<i>.content.<j>` を抽出できること。
- 単体テスト: `recordInvalidThinkingSignatureFromError` が sent payload から `signature` を特定できること（境界・不正入力）。
- 単体テスト: 無効署名キャッシュ（TTL=3時間・セッション分離・ヒット時の局所ストリップ）を固定する。
- 単体テスト: フォールバックの `stripClaudeThinkingBlocksForRetry`（置換・プレースホルダ）。
- 結合: `Invalid signature` を意図的に返す（または不正な `signature` を含む）リクエストを送り、初回でキャッシュ登録→2回目で成功→以後は1回で通ることを確認する。
