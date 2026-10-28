package openai

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestResponsesSSEJSONScanMatchesFullScanFork(t *testing.T) {
	frames := []string{
		`data: {"value":"data: quote\" slash\\ text"}`,
		"event: response.delta\r\ndata: {\r\ndata: \"value\":\"retry: x\\\"y\"\r\ndata: }\r\n",
		"\u2003data:\u2003{\"v\":\"日本語 data: \"}\u2003\n",
		"data: \"escaped\\  \ndata: \"\n",
		"event: x\ndata:\n: ignored \"\ndata: [\"value\"]\n",
		"data: plain \"not json\n",
	}
	for _, frame := range frames {
		var scan responsesSSEJSONScan
		for end := 0; end <= len(frame); end++ {
			pending := []byte(frame[:end])
			got := scan.inside(pending)
			if want := responsesSSEDataEndsInsideJSONString(pending); got != want {
				t.Fatalf("prefix %q: incremental=%v full=%v", pending, got, want)
			}
		}
	}
}

func TestResponsesSSEJSONScanAccumulatorResetFork(t *testing.T) {
	// A field-like continuation inside a string must never become a new SSE line.
	// Two frames exercise prefix consumption, while repeated Flush exercises reset.
	chunks := []string{"data: {\"type\":\"response.output_text.delta\",\"delta\":\"", "data: one", "\\\"", "event: two", "\"}\n\n"}
	for _, images := range []bool{false, true} {
		t.Run(fmt.Sprint(images), func(t *testing.T) {
			var framer responsesSSEFramer
			var accumulator sseFrameAccumulator
			for round := 0; round < 3; round++ {
				var out bytes.Buffer
				for _, chunk := range chunks {
					if images {
						for _, frame := range accumulator.AddChunk([]byte(chunk)) {
							out.Write(frame)
						}
					} else {
						framer.WriteChunk(&out, []byte(chunk))
					}
				}
				if images {
					for _, frame := range accumulator.Flush() {
						out.Write(frame)
					}
				} else {
					framer.Flush(&out)
				}
				if !strings.Contains(out.String(), `"delta":"data: one\"event: two"`) {
					t.Fatalf("round %d: altered string: %q", round, out.String())
				}
			}
		})
	}
}

func TestResponsesSSEJSONScanFragmentParityFork(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tokens := []string{"data:", "event:", "id:", "\n", "\r", " ", "\t", "\u2003", "日本語", "{", "[", "}", "]", "\"", "\\", "x"}
	for trial := 0; trial < 500; trial++ {
		frame := "data: {\"v\":\""
		for i := 0; i < 40; i++ {
			frame += tokens[rng.Intn(len(tokens))]
		}
		var scan responsesSSEJSONScan
		for end := 0; end <= len(frame); end++ {
			pending := []byte(frame[:end])
			if got, want := scan.inside(pending), responsesSSEDataEndsInsideJSONString(pending); got != want {
				t.Fatalf("trial %d prefix %q: incremental=%v full=%v", trial, pending, got, want)
			}
		}
	}
}

func BenchmarkResponsesSSEJSONScanFork(b *testing.B) {
	for _, size := range []int{1024, 8192, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			data := []byte("data: {\"v\":\"" + strings.Repeat("data: x", size/7))
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var scan responsesSSEJSONScan
				for end := 1; end <= len(data); end += 7 {
					scan.inside(data[:end])
				}
			}
		})
	}
}
