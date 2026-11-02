package helps

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAntigravityRequestValidationPreservesTransportBytesFork(t *testing.T) {
	payload := []byte(" {\"request\":{\"contents\":[],\"generationConfig\":{\"responseSchema\":{\"additionalProperties\":false}}}} \n")
	req, err := NewAntigravityRequestWithValidatedSchemas(context.Background(), http.MethodPost, "https://example.invalid", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("validation consumed or changed the transport body")
	}
}

func TestAntigravityRequestValidationRejectsFalseSchemaFork(t *testing.T) {
	payload := []byte(`{"request":{"generationConfig":{"responseSchema":{"anyOf":[false]}}}}`)
	req, err := NewAntigravityRequestWithValidatedSchemas(context.Background(), http.MethodPost, "https://example.invalid", bytes.NewReader(payload))
	status, ok := err.(interface{ StatusCode() int })
	if req != nil || !ok || status.StatusCode() != http.StatusBadRequest {
		t.Fatalf("req=%v err=%v", req, err)
	}
}

func TestAntigravityByteReaderValidationSnapshotFork(t *testing.T) {
	for _, payload := range []string{`{"request":{"contents":[]}}`, `{"request":{"generationConfig":{"responseSchema":false}}}`, ""} {
		for _, prefix := range []string{"", "ignored-prefix"} {
			original := []byte(prefix + payload)
			before := bytes.Clone(original)
			reader := bytes.NewReader(original)
			if _, err := reader.Seek(int64(len(prefix)), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			handled, err := validateAntigravityByteReaderFork(reader)
			if !handled {
				t.Fatal("bytes reader unexpectedly used copying fallback")
			}
			wantReject := strings.Contains(payload, "false")
			if (err != nil) != wantReject {
				t.Fatalf("payload %q error %v", payload, err)
			}
			pos, errSeek := reader.Seek(0, io.SeekCurrent)
			if errSeek != nil || pos != int64(len(prefix)) || !bytes.Equal(original, before) {
				t.Fatal("validation changed source position or bytes")
			}
		}
	}
}

func TestAntigravityValidationWriteShapeFork(t *testing.T) {
	payload := []byte(`{"request":{"generationConfig":{"responseSchema":false}}}`)
	writer := antigravitySchemaValidationWriter{expected: len(payload)}
	if n, err := writer.Write(payload[:len(payload)-1]); n != 0 || err != errAntigravitySchemaWriteShape || writer.seen {
		t.Fatal("partial write was accepted")
	}
	if n, err := writer.Write(payload); n != len(payload) || err != nil || writer.validationErr == nil {
		t.Fatal("complete unsafe payload was not rejected")
	}
	if n, err := writer.Write(payload); n != 0 || err != errAntigravitySchemaWriteShape {
		t.Fatal("repeated write was accepted")
	}
}

func TestAntigravityRequestValidationReplayFallbackFork(t *testing.T) {
	for _, payload := range []string{`{"request":{"contents":[]}}`, `{"request":{"generationConfig":{"responseSchema":false}}}`} {
		reader := strings.NewReader(payload)
		req, err := NewAntigravityRequestWithValidatedSchemas(context.Background(), http.MethodPost, "https://example.invalid", reader)
		if reader.Len() != len(payload) {
			t.Fatal("fallback consumed original reader")
		}
		if strings.Contains(payload, "false") {
			status, ok := err.(interface{ StatusCode() int })
			if req != nil || !ok || status.StatusCode() != http.StatusBadRequest {
				t.Fatalf("req=%v err=%v", req, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			got, errRead := io.ReadAll(req.Body)
			errClose := req.Body.Close()
			if errRead != nil || errClose != nil || string(got) != payload {
				t.Fatal("fallback changed body")
			}
		}
	}
}

func BenchmarkAntigravityRequestValidationNoCopyFork(b *testing.B) {
	payload := []byte(`{"request":{"contents":[{"text":"` + strings.Repeat("x", 1024*1024) + `"}]}}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for b.Loop() {
		req, err := NewAntigravityRequestWithValidatedSchemas(context.Background(), http.MethodPost, "https://example.invalid", bytes.NewReader(payload))
		if err != nil {
			b.Fatal(err)
		}
		if err := req.Body.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
