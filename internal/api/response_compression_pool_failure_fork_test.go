package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestResponseCompressionPoolFailureRespectsIdentityFork(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			original := responseCompressorPools[encoding]
			gets := 0
			responseCompressorPools[encoding] = &deterministicResponseCompressorPool{newCompressor: func() any { gets++; return nil }}
			t.Cleanup(func() { responseCompressorPools[encoding] = original })
			for _, rejectIdentity := range []bool{false, true} {
				name := "allowed"
				accept := encoding
				if rejectIdentity {
					name = "forbidden"
					accept += ", identity;q=0"
				}
				t.Run(name, func(t *testing.T) {
					gets = 0
					body := strings.Repeat("x", 4096)
					router := gin.New()
					router.Use(responseCompressionMiddleware(&config.Config{}))
					router.GET("/", func(c *gin.Context) {
						c.Header("Content-Type", "text/plain")
						c.Header("Content-Length", "4096")
						c.Header("ETag", "original")
						if _, err := c.Writer.WriteString(body); err != nil {
							t.Fatal(err)
						}
						if rejectIdentity {
							c.Writer.WriteHeaderNow()
							c.Writer.Flush()
							if _, err := c.Writer.WriteString("late"); err != nil {
								t.Fatal(err)
							}
						}
					})
					req := httptest.NewRequest(http.MethodGet, "/", nil)
					req.Header.Set("Accept-Encoding", accept)
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, req)
					if gets != 1 {
						t.Errorf("pool gets = %d, want 1", gets)
					}
					wantStatus, wantBody := http.StatusOK, body
					if rejectIdentity {
						wantStatus, wantBody = http.StatusNotAcceptable, ""
						for _, header := range []string{"Content-Length", "Content-Type", "ETag"} {
							if got := recorder.Header().Get(header); got != "" {
								t.Errorf("rejected response %s = %q", header, got)
							}
						}
					}
					if recorder.Code != wantStatus || recorder.Body.String() != wantBody {
						t.Errorf("status/body length = %d/%d, want %d/%d", recorder.Code, recorder.Body.Len(), wantStatus, len(wantBody))
					}
					if got := recorder.Header().Get("Content-Encoding"); got != "" {
						t.Errorf("Content-Encoding = %q", got)
					}
				})
			}
		})
	}
}
