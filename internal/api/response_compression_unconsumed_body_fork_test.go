package api

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

// The fork's empty 406 must still reach the upstream unread-body close hooks,
// including both compression wrappers and the optional request logger.
func TestResponseCompressionRejectionClosesIncompleteBodyFork(t *testing.T) {
	for _, mode := range []struct {
		name       string
		requestLog bool
		commercial bool
	}{
		{name: "error-only logging"},
		{name: "full logging", requestLog: true},
		{name: "commercial mode", commercial: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			logsDir := t.TempDir()
			cfg := &config.Config{CommercialMode: mode.commercial}
			cfg.RequestLog = mode.requestLog
			srv := newTestServerWithConfig(t, cfg, WithRequestLoggerFactory(func(c *config.Config, configPath string) logging.RequestLogger {
				return logging.NewFileRequestLogger(c.RequestLog, logsDir, filepath.Dir(configPath), c.ErrorLogsMaxFiles)
			}))
			srv.engine.POST("/fork-incomplete-binary", func(c *gin.Context) {
				// Deliberately respond without consuming the request body.
				c.Data(http.StatusOK, "application/octet-stream", []byte("must not leak"))
			})
			server := httptest.NewServer(srv.server.Handler)
			defer server.Close()
			addr := strings.TrimPrefix(server.URL, "http://")
			conn, errDial := net.DialTimeout("tcp", addr, 2*time.Second)
			if errDial != nil {
				t.Fatal(errDial)
			}
			defer conn.Close()
			// Bound network failure without relying on sleep or timing assertions.
			if errDeadline := conn.SetDeadline(time.Now().Add(2 * time.Second)); errDeadline != nil {
				t.Fatal(errDeadline)
			}
			request := fmt.Sprintf("POST /fork-incomplete-binary HTTP/1.1\r\nHost: %s\r\nAccept-Encoding: gzip, identity;q=0\r\nContent-Type: application/octet-stream\r\nContent-Length: 128\r\n\r\nx", addr)
			if _, errWrite := io.WriteString(conn, request); errWrite != nil {
				t.Fatal(errWrite)
			}
			raw, errRead := io.ReadAll(conn)
			if errRead != nil {
				t.Fatalf("incomplete body prevented prompt response/EOF: %v; response=%q", errRead, raw)
			}
			response, errParse := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), &http.Request{Method: http.MethodPost})
			if errParse != nil {
				t.Fatalf("parse response: %v; response=%q", errParse, raw)
			}
			defer response.Body.Close()
			body, errBody := io.ReadAll(response.Body)
			if errBody != nil {
				t.Fatal(errBody)
			}
			if response.StatusCode != http.StatusNotAcceptable || len(body) != 0 {
				t.Fatalf("response status/body = %d/%q, want empty 406", response.StatusCode, body)
			}
			if !response.Close || !strings.Contains(strings.ToLower(string(raw)), "\r\nconnection: close\r\n") {
				t.Fatalf("missing Connection: close: %v", response.Header)
			}
			if !strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
				t.Fatalf("missing compression Vary: %v", response.Header)
			}
			if response.Header.Get("Content-Encoding") != "" {
				t.Fatalf("406 retained content encoding: %v", response.Header)
			}
		})
	}
}
