package gemini

import (
	"bytes"
	"context"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
)

// PassthroughGeminiResponseStream extracts SSE event data or forwards bare Gemini payloads.
func PassthroughGeminiResponseStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) [][]byte {
	if payload, isSSE := translatorcommon.SSEEventPayload(rawJSON); isSSE {
		if len(payload) == 0 {
			return [][]byte{}
		}
		rawJSON = payload
	}

	if bytes.Equal(rawJSON, []byte("[DONE]")) {
		return [][]byte{}
	}

	return [][]byte{rawJSON}
}

// PassthroughGeminiResponseNonStream forwards Gemini responses unchanged.
func PassthroughGeminiResponseNonStream(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, _ *any) []byte {
	return rawJSON
}

func GeminiTokenCount(ctx context.Context, count int64) []byte {
	return translatorcommon.GeminiTokenCountJSON(count)
}
