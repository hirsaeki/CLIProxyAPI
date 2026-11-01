package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestExecutorSSEDeframerMetadataFieldsFork(t *testing.T) {
	for _, metadata := range []string{"id: event-1", "retry: 1000", "id", "retry", "event", "data"} {
		for _, newline := range []string{"\n", "\r\n"} {
			frame := metadata + newline + "data: {\"choices\":[]}" + newline + newline
			done := "data: [DONE]" + newline + newline
			input := frame + done
			t.Run(fmt.Sprintf("%s/%q", metadata, newline), func(t *testing.T) {
				check := func(t *testing.T, cuts []int) {
					t.Helper()
					got, tail := collectDeframerOutput(t, input, cuts)
					assertStringsEqual(t, "events", got, []string{frame, done})
					if tail != "" {
						t.Fatalf("flush = %q, want empty", tail)
					}
				}
				t.Run("coalesced", func(t *testing.T) { check(t, nil) })
				for cut := 1; cut < len(input); cut++ {
					t.Run(fmt.Sprintf("split-%d", cut), func(t *testing.T) { check(t, []int{cut}) })
				}
				var cuts []int
				for cut := 1; cut < len(input); cut++ {
					cuts = append(cuts, cut)
				}
				t.Run("bytewise", func(t *testing.T) { check(t, cuts) })
			})
		}
	}
}

func TestExecutorAdapterSSEMetadataFieldsFork(t *testing.T) {
	const payload = `{"choices":[{"delta":{"content":"Hello"}}]}`
	const finish = `{"choices":[{"delta":{},"finish_reason":"stop"}]}`
	for _, metadata := range []string{"id: event-1", "retry: 1000", "id", "retry", "event", "data"} {
		// Include metadata both before the first event and after SSE mode has
		// been established. Only JSON payloads should reach the HTTP handler.
		input := metadata + "\ndata: " + payload + "\n\n" + metadata + "\ndata: " + finish + "\n\ndata: [DONE]\n\n"
		for _, format := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatGemini} {
			for _, bytewise := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/bytewise-%t", metadata, format, bytewise), func(t *testing.T) {
					chunks := [][]byte{[]byte(input)}
					if bytewise {
						chunks = nil
						for i := range input {
							chunks = append(chunks, []byte(input[i:i+1]))
						}
					}
					adapter := streamAdapterForTest(t, format, chunks...)
					got, err := executeSameFormatStream(t, adapter, format)
					if err != nil {
						t.Fatalf("stream returned error: %v", err)
					}
					assertStringsEqual(t, "payloads", got, []string{payload, finish})
				})
			}
		}
	}

}

func TestExecutorSSEDeframerMetadataLikeBarePayloadsFork(t *testing.T) {
	for _, payload := range []string{"identity", "retrying", "id", "retry"} {
		t.Run(payload, func(t *testing.T) {
			deframer := &executorSSEDeframer{}
			var got []string
			for _, chunk := range deframer.feed([]byte(payload)) {
				got = append(got, string(chunk))
			}
			if tail := deframer.flush(); len(tail) > 0 {
				got = append(got, string(tail))
			}
			assertStringsEqual(t, "bare payload", got, []string{payload})
		})
	}
}

func TestExecutorAdapterSSEMetadataTerminalFork(t *testing.T) {
	for _, format := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			adapter := streamAdapterForTest(t, format,
				[]byte("id: 1\n\nretry: 1000\n\ndata: {\"choices\":[]}\nid: 2\n\nretry: 1000\ndata: [DONE]\n\n"))
			got, err := executeSameFormatStream(t, adapter, format)
			if err != nil {
				t.Fatal(err)
			}
			assertStringsEqual(t, "metadata and terminal", got, []string{`{"choices":[]}`})
		})
	}
	t.Run("chat drops data after terminal", func(t *testing.T) {
		adapter := streamAdapterForTest(t, sdktranslator.FormatOpenAI,
			[]byte("id: done\ndata: [DONE]\n\ndata: {\"choices\":[]}\n\n"))
		got, err := executeSameFormatStream(t, adapter, sdktranslator.FormatOpenAI)
		if err != nil {
			t.Fatal(err)
		}
		assertStringsEqual(t, "after terminal", got, nil)
	})
}

func TestExecutorAdapterSSEColonlessTailFork(t *testing.T) {
	for _, field := range []string{"id", "retry", "event", "data"} {
		for _, ending := range []string{"", "\n", "\r\n"} {
			for _, format := range []sdktranslator.Format{sdktranslator.FormatOpenAI, sdktranslator.FormatGemini} {
				t.Run(fmt.Sprintf("%s/%q/%s", field, ending, format), func(t *testing.T) {
					adapter := streamAdapterForTest(t, format, []byte(field+ending))
					got, err := executeSameFormatStream(t, adapter, format)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					if ending == "" {
						want = []string{field}
					}
					assertStringsEqual(t, "tail", got, want)
				})
			}
		}
	}
}

func TestExecutorSSEColonlessErrorTailFork(t *testing.T) {
	streamErr := errors.New("stream failure")
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", ending), func(t *testing.T) {
			in := make(chan pluginapi.ExecutorStreamChunk, 2)
			in <- pluginapi.ExecutorStreamChunk{Payload: []byte("id" + ending)}
			in <- pluginapi.ExecutorStreamChunk{Err: streamErr}
			close(in)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := deframeExecutorSSEChunks(ctx, in)
			first, ok := <-out
			if !ok || first.Err != nil || string(first.Payload) != "id"+ending {
				t.Fatalf("metadata tail lost framing: %#v, open=%t", first, ok)
			}
			failure, ok := <-out
			if !ok || failure.Err != streamErr {
				t.Fatalf("error = %#v, open=%t", failure, ok)
			}
			if _, ok := <-out; ok {
				t.Fatal("unexpected extra chunk")
			}
		})
	}
}
