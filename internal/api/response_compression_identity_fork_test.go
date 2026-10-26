package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestResponseCompressionRejectsUnacceptableFallbackFork(t *testing.T) {
	for _, tc := range []struct {
		name, accept, contentType, method string
		encodings                         []string
		status                            int
		earlyFlush                        bool
		wantStatus                        int
		wantEncoding                      string
	}{
		{name: "binary", contentType: "application/octet-stream", wantStatus: 406},
		{name: "binary early flush", contentType: "application/octet-stream", earlyFlush: true, wantStatus: 406},
		{name: "partial", status: 206, wantStatus: 406},
		{name: "explicit identity", encodings: []string{"identity"}, wantStatus: 406},
		{name: "unaccepted encoding", encodings: []string{"br"}, wantStatus: 406},
		{name: "accepted lower ranked encoding", accept: "gzip, br;q=0.5, identity;q=0", encodings: []string{"br"}, wantStatus: 200, wantEncoding: "br"},
		{name: "stacked accepted encoding", accept: "gzip, br, identity;q=0", encodings: []string{"gzip", "br"}, wantStatus: 200, wantEncoding: "gzip,br"},
		{name: "stacked rejected encoding", accept: "gzip, br;q=0, identity;q=0", encodings: []string{"gzip", "br"}, wantStatus: 406},
		{name: "wildcard accepted existing", accept: "gzip, *;q=0.5, identity;q=0", encodings: []string{"br"}, wantStatus: 200, wantEncoding: "br"},
		{name: "bodyless 204", status: 204, wantStatus: 204},
		{name: "bodyless 304", status: 304, wantStatus: 304},
		{name: "HEAD", method: http.MethodHead, contentType: "application/octet-stream", wantStatus: 200},
		{name: "identity permitted", accept: "gzip", contentType: "application/octet-stream", wantStatus: 200},
		{name: "compressible", wantStatus: 200, wantEncoding: "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(responseCompressionMiddleware(&config.Config{}))
			router.Any("/", func(c *gin.Context) {
				contentType := tc.contentType
				if contentType == "" {
					contentType = "application/json"
				}
				c.Header("Content-Type", contentType)
				c.Header("Content-Length", "2")
				c.Header("Content-Range", "bytes 0-1/2")
				for _, encoding := range tc.encodings {
					c.Writer.Header().Add("Content-Encoding", encoding)
				}
				if tc.status != 0 {
					c.Status(tc.status)
				}
				if tc.status == 204 || tc.status == 304 || tc.method == http.MethodHead {
					return
				}
				if tc.earlyFlush {
					c.Writer.Flush()
				}
				if _, err := c.Writer.WriteString("{}"); err != nil {
					t.Fatal(err)
				}
				if tc.wantStatus == 406 {
					c.Writer.WriteHeaderNow()
					c.Writer.Flush()
					if _, err := c.Writer.WriteString("must not leak"); err != nil {
						t.Fatal(err)
					}
				}
				if tc.wantStatus == 406 && c.Writer.Status() != 406 {
					t.Errorf("writer status=%d", c.Writer.Status())
				}
			})
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/", nil)
			accept := tc.accept
			if accept == "" {
				accept = "gzip, identity;q=0"
			}
			req.Header.Set("Accept-Encoding", accept)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d", response.Code, tc.wantStatus)
			}
			if got := strings.Join(response.Result().Header.Values("Content-Encoding"), ","); got != tc.wantEncoding {
				t.Errorf("encoding=%q want=%q", got, tc.wantEncoding)
			}
			if !strings.Contains(response.Result().Header.Get("Vary"), "Accept-Encoding") {
				t.Error("missing Vary")
			}
			if tc.wantStatus == 406 {
				if response.Body.Len() != 0 {
					t.Errorf("unacceptable body leaked: %q", response.Body.String())
				}
				for _, name := range []string{"Content-Length", "Content-Range", "Content-Type"} {
					if response.Result().Header.Get(name) != "" {
						t.Errorf("stale %s", name)
					}
				}
			}
		})
	}
}
