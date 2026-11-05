package kvstore

import (
	"bytes"
	"testing"
)

func TestEncodeEnvelopeOwnsBufferFork(t *testing.T) {
	original := []byte(`{"value":"original"}`)
	// Spare capacity would allow an unsafe encoder to reuse the caller's array.
	value := make([]byte, len(original), len(original)+envelopeHeaderSize+32)
	copy(value, original)
	envelope := encodeEnvelope(value, 123)
	if !bytes.Equal(value, original) || !bytes.Equal(envelope[envelopeHeaderSize:], original) {
		t.Fatal("encoding changed the caller's bytes or the stored payload")
	}
	value[0] = '!'
	if envelope[envelopeHeaderSize] != original[0] {
		t.Fatal("caller mutation changed the published envelope")
	}
	envelope[envelopeHeaderSize+1] = '!'
	if value[1] != original[1] {
		t.Fatal("envelope mutation changed the caller's buffer")
	}
}
