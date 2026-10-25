package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDevinCallbackNilHandlerFork(t *testing.T) {
	var h *Handler
	if callback, errCallback := h.devinCallbackURL(); errCallback == nil || callback != "" {
		t.Fatalf("nil handler callback = %q, %v; want configuration error", callback, errCallback)
	}
}

func TestOAuthStartDomainChannelFallbackFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name, body, want string
		status           int
	}{
		{"empty domain", `{"domain":"","channel":" kimi.com "}`, "kimi.com", http.StatusBadRequest},
		{"blank domain", `{"domain":"  ","channel":"kimi.com"}`, "kimi.com", http.StatusBadRequest},
		{"null domain", `{"domain":null,"channel":"kimi.com"}`, "kimi.com", http.StatusBadRequest},
		{"domain wins", `{"domain":" moonshot.cn ","channel":"kimi.com"}`, "moonshot.cn", http.StatusBadRequest},
		{"domain wins over invalid alias", `{"domain":"moonshot.cn","channel":12}`, "moonshot.cn", http.StatusBadRequest},
		{"invalid domain", `{"domain":12,"channel":"kimi.com"}`, "", http.StatusBadRequest},
		{"invalid fallback channel", `{"domain":"","channel":12}`, "", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/oauth/auth-url", strings.NewReader(tt.body))
			(&Handler{}).StartOAuthV8(c)
			if tt.name == "invalid domain" && !strings.Contains(rec.Body.String(), "invalid domain") {
				t.Fatalf("invalid domain was not rejected: %s", rec.Body.String())
			}
			if tt.name == "invalid fallback channel" && !strings.Contains(rec.Body.String(), "invalid channel") {
				t.Fatalf("invalid fallback channel was not rejected: %s", rec.Body.String())
			}
			if rec.Code != tt.status || oauthSessionDomainFromContext(c) != tt.want {
				t.Fatalf("status=%d domain=%q, want %d %q; body=%s", rec.Code, oauthSessionDomainFromContext(c), tt.status, tt.want, rec.Body.String())
			}
		})
	}
}

func TestOAuthStartBodyLimitFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const limit = 1 << 20
	prefix, suffix := `{"domain":"marker","padding":"`, `"}`
	for _, tt := range []struct {
		name       string
		size       int
		outerLimit int64
		status     int
		parsed     bool
	}{
		{"exact limit", limit, 0, http.StatusBadRequest, true},
		{"over limit", limit + 1, 0, http.StatusRequestEntityTooLarge, false},
		{"stricter middleware", 128, 64, http.StatusRequestEntityTooLarge, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := prefix + strings.Repeat("x", tt.size-len(prefix)-len(suffix)) + suffix
			c.Request = httptest.NewRequest(http.MethodPost, "/v8/management/oauth/auth-url", strings.NewReader(body))
			c.Request.ContentLength = -1 // Exercise streamed bodies, not just a Content-Length precheck.
			if tt.outerLimit > 0 {
				c.Request.Body = http.MaxBytesReader(rec, c.Request.Body, tt.outerLimit)
			}
			(&Handler{}).StartOAuthV8(c)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if got := oauthSessionDomainFromContext(c); (got == "marker") != tt.parsed {
				t.Fatalf("parsed domain=%q, want parsed=%v", got, tt.parsed)
			}
		})
	}
}
