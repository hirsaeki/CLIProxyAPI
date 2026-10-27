package middleware

import (
	"bytes"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

type queuedChunkLoggerFork struct {
	testStreamingLogWriter
	body bytes.Buffer
}

func (w *queuedChunkLoggerFork) WriteChunkAsync(chunk []byte) { _, _ = w.body.Write(chunk) }

func TestStreamingLoggerDelayedWorkerPreservesQueuedChunksFork(t *testing.T) {
	chunks := make(chan []byte, 3)
	for _, chunk := range []string{"first", "second", "third"} {
		chunks <- []byte(chunk)
	}
	logger := &queuedChunkLoggerFork{}
	wrapper := &ResponseWriterWrapper{chunkChannel: chunks, streamWriter: logger}
	done := make(chan struct{})
	start := make(chan struct{})
	// Delay scheduling until the owner has closed the queue and released its fields.
	// The worker must use the references captured when its goroutine was launched.
	go func(done chan struct{}, queue <-chan []byte, writer logging.StreamingLogWriter) {
		<-start
		wrapper.processStreamingChunks(done, queue, writer)
	}(done, wrapper.chunkChannel, wrapper.streamWriter)
	close(chunks)
	wrapper.chunkChannel = nil
	wrapper.streamWriter = nil
	close(start)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delayed worker failed to complete")
	}
	if got := logger.body.String(); got != "firstsecondthird" {
		t.Fatalf("queued chunks=%q; want all three in order", got)
	}
}
