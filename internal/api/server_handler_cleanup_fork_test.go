package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// newJoinedHTTPTestServerFork waits for hijacked request handlers, which
// httptest.Server.Close does not join. Callers must close their WebSocket
// connections before cleanup so request logging finishes before TempDir removal.
func newJoinedHTTPTestServerFork(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	var requests sync.WaitGroup
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		defer requests.Done()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		server.Close()
		done := make(chan struct{})
		go func() {
			requests.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("HTTP handlers did not finish after closing test connections")
		}
	})
	return server
}
