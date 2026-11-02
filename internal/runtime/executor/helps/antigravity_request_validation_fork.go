package helps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type antigravitySchemaRequestError struct{ err error }

func (e antigravitySchemaRequestError) Error() string   { return e.err.Error() }
func (e antigravitySchemaRequestError) StatusCode() int { return http.StatusBadRequest }

// NewAntigravityRequestWithValidatedSchemas validates the serialized transport body
// without rerunning payload rules or changing the bytes sent to the provider.
func NewAntigravityRequestWithValidatedSchemas(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if byteReader, ok := body.(*bytes.Reader); ok {
		if handled, err := validateAntigravityByteReaderFork(byteReader); handled {
			if err != nil {
				return nil, antigravitySchemaRequestError{err}
			}
			return req, nil
		}
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("antigravity: serialized request body must be replayable")
	}
	reader, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := ValidateAntigravityFalseSchemas(payload); err != nil {
		return nil, antigravitySchemaRequestError{err}
	}
	return req, nil
}

// A bytes.Reader snapshot exposes its unread bytes synchronously through WriteTo
// without consuming the transport reader. Accept only a single complete write;
// if that contract changes, the caller falls back to the replayable body copy.
func validateAntigravityByteReaderFork(reader *bytes.Reader) (bool, error) {
	snapshot := *reader
	expected := snapshot.Len()
	if expected == 0 {
		return true, ValidateAntigravityFalseSchemas(nil)
	}
	writer := antigravitySchemaValidationWriter{expected: expected}
	written, err := snapshot.WriteTo(&writer)
	if err != nil || written != int64(expected) || !writer.seen {
		return false, nil
	}
	return true, writer.validationErr
}

var errAntigravitySchemaWriteShape = errors.New("antigravity: expected one complete validation write")

// antigravitySchemaValidationWriter accepts one complete serialized body.
type antigravitySchemaValidationWriter struct {
	expected      int
	seen          bool
	validationErr error
}

// Write validates synchronously and retains neither p nor its backing storage.
func (w *antigravitySchemaValidationWriter) Write(p []byte) (int, error) {
	if w.seen || len(p) != w.expected {
		return 0, errAntigravitySchemaWriteShape
	}
	w.seen = true
	w.validationErr = ValidateAntigravityFalseSchemas(p)
	return len(p), nil
}
