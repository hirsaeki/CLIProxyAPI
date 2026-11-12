package helps

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

func TestClaudeThreadPartialTerminalReadErrorFork(t *testing.T) {
	const start = "data: {\"type\":\"message_start\",\"message\":{\"id\":\"message-partial\"}}\n\n"
	for _, terminal := range []struct {
		name string
		line string
	}{
		{name: "message_stop", line: "data: {\"type\":\"message_stop\"}"},
		{name: "complete_message", line: "data: {\"type\":\"message\",\"id\":\"message-partial\",\"stop_reason\":\"end_turn\"}"},
	} {
		for _, suffix := range []string{"", "\n"} {
			name := terminal.name + "/no_newline"
			if suffix != "" {
				name = terminal.name + "/no_delimiter"
			}
			t.Run(name, func(t *testing.T) {
				store := &ClaudeThreadBetaStore{}
				request, _, errBind := store.Bind(t.Name(), "", http.Header{"Anthropic-Beta": []string{"profile"}}, ClaudeThreadBetaPolicy{})
				if errBind != nil || request == nil {
					t.Fatalf("bind: request=%v, error=%v", request, errBind)
				}
				beforeComplete, onCommit := 0, 0
				request.OnCommit(func(string) { onCommit++ })
				readErr := errors.New("terminal transport failure")
				body := io.MultiReader(strings.NewReader(start+terminal.line+suffix), iotest.ErrReader(readErr))
				data, errRead := io.ReadAll(request.StreamReader(body, func(string) { beforeComplete++ }))
				if !errors.Is(errRead, readErr) {
					t.Fatalf("read error = %v, want transport failure", errRead)
				}
				if beforeComplete != 0 || onCommit != 0 {
					t.Fatalf("failed terminal committed: beforeComplete=%d, onCommit=%d", beforeComplete, onCommit)
				}
				if string(data) != start {
					t.Fatalf("failed terminal escaped reader: got %q, want only message_start", data)
				}
				if _, found, _ := store.Bind(t.Name(), "message-partial", http.Header{}, ClaudeThreadBetaPolicy{}); found {
					t.Fatal("failed terminal stored a beta profile")
				}
			})
		}
	}
}

func TestClaudeThreadDrainedBufferRetentionFork(t *testing.T) {
	const stop = "data: {\"type\":\"message_stop\"}\n\n"
	padding := strings.Repeat("x", 128*1024)
	ping := "data: {\"type\":\"ping\",\"padding\":\"" + padding + "\"}\n\n"
	terminal := "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"padding\":\"" + padding + "\"}\n\n"
	failure := "data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n"
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "ready_drained", body: ping + stop, want: ping + stop},
		{name: "pending_released", body: terminal + stop, want: terminal + stop},
		{name: "pending_discarded_on_error", body: terminal + failure + stop, want: failure + stop},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(test.body))
			scanner.Buffer(nil, claudeThreadEventLimit)
			reader := &claudeThreadStreamReader{scanner: scanner, commit: func(string) {}}
			data, errRead := io.ReadAll(reader)
			if errRead != nil || string(data) != test.want {
				t.Fatalf("read: error=%v, output matches=%t", errRead, string(data) == test.want)
			}
			if reader.ready.Len() != 0 || reader.pending.Len() != 0 {
				t.Fatalf("drained reader retains contents: ready=%d, pending=%d", reader.ready.Len(), reader.pending.Len())
			}
			if reader.ready.Cap() > 64*1024 || reader.pending.Cap() > 64*1024 {
				t.Fatalf("drained reader retains large buffers: ready=%d, pending=%d", reader.ready.Cap(), reader.pending.Cap())
			}
		})
	}
}
