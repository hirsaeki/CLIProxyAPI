package management

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

type verifyBodyReaderFork struct {
	io.Reader
	read int
}

func (r *verifyBodyReaderFork) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}
func (r *verifyBodyReaderFork) Close() error { return nil }

type verifyBodyErrorReaderFork struct{ err error }

func (r verifyBodyErrorReaderFork) Read([]byte) (int, error) { return 0, r.err }

func TestVerifyAuthFileBodyLimitFork(t *testing.T) {
	const limit = 1 << 20
	prefix, suffix := `{"name":"missing.json","padding":"`, `"}`
	for _, tc := range []struct {
		name   string
		size   int
		length int64
		outer  int64
		want   int
	}{
		{"exact known", limit, limit, 0, http.StatusNotFound},
		{"exact streamed", limit, -1, 0, http.StatusNotFound},
		{"over known", limit + 1, limit + 1, 0, http.StatusRequestEntityTooLarge},
		{"over streamed", limit + 8192, -1, 0, http.StatusRequestEntityTooLarge},
		{"stricter outer limit", 256, -1, 128, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			raw := prefix + strings.Repeat("x", tc.size-len(prefix)-len(suffix)) + suffix
			reader := &verifyBodyReaderFork{Reader: strings.NewReader(raw)}
			c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/credentials/verify", nil)
			c.Request.Body = reader
			c.Request.ContentLength = tc.length
			if tc.outer > 0 {
				c.Request.Body = http.MaxBytesReader(rec, c.Request.Body, tc.outer)
			}
			(&Handler{authManager: coreauth.NewManager(nil, nil, nil)}).VerifyAuthFileV8(c)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d response=%s", rec.Code, tc.want, rec.Body.String())
			}
			bound := int64(limit)
			if tc.outer > 0 {
				bound = tc.outer
			}
			if int64(reader.read) > bound+1 {
				t.Fatalf("read %d bytes, limit+sentinel=%d", reader.read, bound+1)
			}
			if tc.want == http.StatusNotFound && !strings.Contains(rec.Body.String(), "auth file not found") {
				t.Fatalf("valid body was not parsed: %s", rec.Body.String())
			}
		})
	}
}

func TestVerifyAuthFileEmptyAndReadErrorFork(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		body      io.Reader
		want      int
		message   string
	}{
		{"empty no query", "/verify", nil, http.StatusBadRequest, "name or auth_index is required"},
		{"empty query", "/verify?name=missing.json", nil, http.StatusNotFound, "auth file not found"},
		{"whitespace query", "/verify?auth_index=missing", strings.NewReader(" \t\r\n"), http.StatusNotFound, "auth file not found"},
		{"read error", "/verify?name=missing.json", verifyBodyErrorReaderFork{errors.New("reader failure")}, http.StatusBadRequest, "invalid request body"},
		{"cancellation", "/verify?name=missing.json", verifyBodyErrorReaderFork{context.Canceled}, 499, "invalid request body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.url, tc.body)
			(&Handler{authManager: coreauth.NewManager(nil, nil, nil)}).VerifyAuthFileV8(c)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("status=%d body=%s want=%d %q", rec.Code, rec.Body.String(), tc.want, tc.message)
			}
		})
	}
}
