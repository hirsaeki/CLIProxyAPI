package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestVertexFailoverManagementAccessFork(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "synthetic-management-password")
	cfg := &config.Config{}
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: management.NewHandlerWithoutConfigFilePath(cfg, nil)}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		for _, tc := range []struct {
			prefix string
			key    string
			want   int
		}{
			{"/v8/management", "", http.StatusUnauthorized},
			{"/v8/management", "synthetic-management-password", http.StatusServiceUnavailable},
			{"/v0/management", "synthetic-management-password", http.StatusNotFound},
		} {
			path := tc.prefix + "/credentials/vertex-failover?name=vertex-fake.json"
			req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"vertex-fake.json","mode":"off"}`))
			if tc.key != "" {
				req.Header.Set("Authorization", "Bearer "+tc.key)
			}
			rec := httptest.NewRecorder()
			s.engine.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("%s %s status=%d want=%d", method, path, rec.Code, tc.want)
			}
		}
	}
}
