package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the read contract where a reader returns its final bytes with EOF.
type finalEOFReaderFork struct{ *bytes.Reader }

func (r finalEOFReaderFork) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.Len() == 0 {
		return n, io.EOF
	}
	return n, err
}

func TestDeferredCaptureWithoutFileFactoryBoundsMemoryFork(t *testing.T) {
	limit := int(maxErrorOnlyCapturedRequestBodyBytes)
	for _, tc := range []struct {
		name              string
		size, chunk       int
		unknown, finalEOF bool
	}{
		{name: "below limit", size: limit - 1, chunk: 8191},
		{name: "exact limit", size: limit, chunk: 8191},
		{name: "cross boundary", size: limit + 1, chunk: 8191},
		{name: "one large read with EOF", size: limit + 7, chunk: limit + 7, finalEOF: true},
		{name: "unknown length crosses boundary", size: limit + 7, chunk: 8191, unknown: true},
		{name: "continued reads after truncation", size: 4*limit + 17, chunk: 65537, unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), tc.size)
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
			if tc.unknown {
				request.ContentLength = -1
			}
			if tc.finalEOF {
				request.Body = io.NopCloser(finalEOFReaderFork{bytes.NewReader(payload)})
			}
			capture := attachDeferredRequestBodyCapture(request, &testRequestLogger{}, &RequestInfo{}, false, false)
			if capture == nil || capture.factory != nil {
				t.Fatal("expected memory-only deferred capture")
			}
			defer capture.Close()
			var received bytes.Buffer
			buf := make([]byte, tc.chunk)
			for {
				n, errRead := request.Body.Read(buf)
				received.Write(buf[:n])
				if capture.memBuf.Len() > limit || capture.bytesCaptured > int64(limit) {
					t.Fatalf("retained %d bytes (%d counted), limit %d", capture.memBuf.Len(), capture.bytesCaptured, limit)
				}
				if errRead == io.EOF {
					break
				}
				if errRead != nil {
					t.Fatal(errRead)
				}
			}
			if !bytes.Equal(received.Bytes(), payload) || capture.bytesRead != int64(len(payload)) {
				t.Fatal("logging changed bytes delivered to the handler")
			}
			captured, marker, errCapture := capture.Bytes()
			if errCapture != nil {
				t.Fatal(errCapture)
			}
			wantLength := min(tc.size, limit)
			if !bytes.Equal(captured, payload[:wantLength]) {
				t.Fatalf("captured prefix length=%d, want=%d", len(captured), wantLength)
			}
			if got := strings.Contains(marker, "REQUEST BODY TRUNCATED"); got != (tc.size > limit) {
				t.Fatalf("wrong truncation marker: %q", marker)
			}
			if strings.Contains(marker, "INCOMPLETE") || !capture.isComplete() {
				t.Fatalf("consumed body marked incomplete: %q", marker)
			}
			if capture.source != nil || capture.file != nil {
				t.Fatal("memory-only capture unexpectedly created a file")
			}
		})
	}
}

func TestDeferredCaptureMemoryTruncationDoesNotDrainRemainderFork(t *testing.T) {
	limit := int(maxErrorOnlyCapturedRequestBodyBytes)
	payload := bytes.Repeat([]byte("y"), limit+9)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	capture := attachDeferredRequestBodyCapture(request, &testRequestLogger{}, &RequestInfo{}, false, false)
	defer capture.Close()
	prefix := make([]byte, limit+1)
	if _, err := io.ReadFull(capture, prefix); err != nil {
		t.Fatal(err)
	}
	captured, marker, err := capture.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != limit || !strings.Contains(marker, "TRUNCATED") || !strings.Contains(marker, "INCOMPLETE") {
		t.Fatalf("capture length=%d marker=%q", len(captured), marker)
	}
	remainder, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(prefix, remainder...), payload) {
		t.Fatal("finalizing log capture drained or changed unread request bytes")
	}
	if capture.bytesCaptured != int64(limit) {
		t.Fatal("capture grew after finalization")
	}
}
