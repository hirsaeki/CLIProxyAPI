// Package chat_completions provides passthrough response translation for OpenAI Chat Completions.
// It extracts OpenAI-compatible SSE event data and drops "[DONE]".
package chat_completions

import (
	"bytes"
	"context"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/common"
)

// ConvertOpenAIResponseToOpenAI normalizes a single chunk of an OpenAI-compatible streaming response.
// If the chunk is an SSE event, only its data payload is returned.
// Metadata-only events and the "[DONE]" marker yield no output.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response (unused in current implementation)
//   - rawJSON: The raw JSON response from the OpenAI API
//   - param: A pointer to a parameter object for maintaining state between calls
//
// Returns:
//   - [][]byte: A slice of JSON payload chunks in OpenAI format.
func ConvertOpenAIResponseToOpenAI(_ context.Context, _ string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	if param != nil {
		if done, ok := (*param).(bool); ok && done {
			// Drop any chunks that arrive after the terminal [DONE] marker.
			return [][]byte{}
		}
	}
	if payload, isSSE := translatorcommon.SSEEventPayload(rawJSON); isSSE {
		if len(payload) == 0 {
			return [][]byte{}
		}
		rawJSON = payload
	}
	if bytes.Equal(rawJSON, []byte("[DONE]")) {
		if param != nil {
			*param = true
		}
		return [][]byte{}
	}
	return [][]byte{rawJSON}
}

// ConvertOpenAIResponseToOpenAINonStream passes through a non-streaming OpenAI response.
//
// Parameters:
//   - ctx: The context for the request, used for cancellation and timeout handling
//   - modelName: The name of the model being used for the response
//   - rawJSON: The raw JSON response from the OpenAI API
//   - param: A pointer to a parameter object for the conversion
//
// Returns:
//   - []byte: The OpenAI-compatible JSON response.
func ConvertOpenAIResponseToOpenAINonStream(ctx context.Context, modelName string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	return rawJSON
}
