package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestWebsocketCompressionIsServerScopedAcrossReloadFork(t *testing.T) {
	enabled, disabled := true, false
	cfgA := &config.Config{}
	cfgA.Codex.WebsocketCompression = &disabled
	serverA := newTestServerWithConfig(t, cfgA)
	httpA := httptest.NewServer(serverA.engine)
	defer httpA.Close()
	cfgB := &config.Config{}
	cfgB.Codex.WebsocketCompression = &enabled
	serverB := newTestServerWithConfig(t, cfgB)
	httpB := httptest.NewServer(serverB.engine)
	defer httpB.Close()

	assertNegotiated := func(server *httptest.Server, want bool) {
		t.Helper()
		dialer := websocket.Dialer{EnableCompression: true}
		conn, response, errDial := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{})
		if errDial != nil {
			t.Fatalf("upgrade: %v", errDial)
		}
		defer conn.Close()
		got := strings.Contains(response.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate")
		if got != want {
			t.Fatalf("compression negotiated = %v, want %v", got, want)
		}
	}
	assertNegotiated(httpA, false)
	assertNegotiated(httpB, true)
	// Reloading either server must not affect the other's future upgrades.
	nextB := *cfgB
	nextB.Codex.WebsocketCompression = &disabled
	serverB.UpdateClients(&nextB)
	assertNegotiated(httpA, false)
	assertNegotiated(httpB, false)
	nextA := *cfgA
	nextA.Codex.WebsocketCompression = &enabled
	serverA.UpdateClients(&nextA)
	assertNegotiated(httpA, true)
	assertNegotiated(httpB, false)
}
