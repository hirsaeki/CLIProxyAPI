package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/tidwall/gjson"
)

func TestResponsesTerminalFrameEndsLocalInterruptBeforeStreamCloseFork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, terminal := range []string{"response.completed", "response.done", "response.incomplete"} {
		t.Run(terminal, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			data := make(chan []byte)
			errs := make(chan *interfaces.ErrorMessage)
			finished := make(chan error, 1)
			local := newResponsesLocalInterrupt()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, errUpgrade := responsesWebsocketUpgrader.Upgrade(w, r, nil)
				if errUpgrade != nil {
					finished <- errUpgrade
					return
				}
				defer func() { _ = conn.Close() }()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r.WithContext(ctx)
				_, _, _, errMsg, errForward := (*OpenAIResponsesAPIHandler)(nil).forwardResponsesWebsocket(
					c, newResponsesWebsocketWriter(conn), func(...interface{}) {}, data, errs,
					nil, "interrupt-lifecycle-fork", responsesWebsocketForwardOptions{localInterrupt: local},
				)
				if errMsg != nil && errForward == nil {
					errForward = fmt.Errorf("unexpected response error: %v", errMsg.Error)
				}
				finished <- errForward
			}))
			t.Cleanup(func() {
				cancel()
				server.Close()
			})
			client, _, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if errDial != nil {
				t.Fatalf("dial websocket: %v", errDial)
			}
			t.Cleanup(func() { _ = client.Close() })
			if errDeadline := client.SetReadDeadline(time.Now().Add(5 * time.Second)); errDeadline != nil {
				t.Fatal(errDeadline)
			}
			sendAndRead := func(event, responseID string) {
				t.Helper()
				payload := []byte(fmt.Sprintf(`{"type":%q,"response":{"id":%q,"output":[]}}`, event, responseID))
				select {
				case data <- payload:
				case errFinished := <-finished:
					t.Fatalf("forwarder ended before %s: %v", event, errFinished)
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out sending %s", event)
				}
				_, got, errRead := client.ReadMessage()
				if errRead != nil {
					t.Fatalf("read %s: %v", event, errRead)
				}
				if gjson.GetBytes(got, "type").String() != event || gjson.GetBytes(got, "response.id").String() != responseID {
					t.Fatalf("expected %s for %s, got %s", event, responseID, got)
				}
			}
			for _, responseID := range []string{"first", "next"} {
				sendAndRead("response.created", responseID)
				local.mu.Lock()
				active := local.active
				local.mu.Unlock()
				if !active {
					t.Fatalf("local cancellation was not enabled for %s", responseID)
				}
				sendAndRead(terminal, responseID)
				// Both source channels remain open. Receiving the terminal frame
				// itself must guarantee that a late interrupt cannot cancel it.
				if local.deliver([]byte(`{"type":"response.interrupt","response_id":"unknown"}`)) {
					t.Fatal("terminal response still accepted a local interrupt")
				}
				select {
				case errFinished := <-finished:
					t.Fatalf("forwarder ended before source closure: %v", errFinished)
				default:
				}
			}
			close(data)
			select {
			case errFinished := <-finished:
				if errFinished != nil {
					t.Fatalf("forwarder failed: %v", errFinished)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("forwarder did not finish after source closure")
			}
		})
	}
}
