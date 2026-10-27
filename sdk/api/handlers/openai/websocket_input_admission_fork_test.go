package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// An idle read can span an upstream loss. It must not stamp a newly received
// recovery request with the generation from before the blocking read began.
func TestWebsocketInputAdmissionAfterIdleReadSpansLossFork(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var epoch atomic.Uint64
	admitted := make(chan (<-chan cliproxyexecutor.WebsocketInput), 1)
	reading := make(chan struct{})
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		conn, errUpgrade := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if errUpgrade != nil {
			t.Error(errUpgrade)
			return
		}
		defer func() {
			if errClose := conn.Close(); errClose != nil {
				t.Logf("close server websocket: %v", errClose)
			}
		}()
		// The ping handler runs inside ReadMessage, giving the test a deterministic
		// barrier after the next read has started but before its text frame arrives.
		conn.SetPingHandler(func(string) error { close(reading); return nil })
		input, done := readResponsesWebsocketInput(ctx, cancel, conn, nil, nil, nil, nil, &epoch)
		admitted <- input
		<-done
	}))
	defer server.Close()
	client, _, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if errDial != nil {
		t.Fatal(errDial)
	}
	defer func() {
		if errClose := client.Close(); errClose != nil {
			t.Logf("close client websocket: %v", errClose)
		}
		<-handlerDone
	}()
	input := <-admitted
	if errWrite := client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","event_id":"before"}`)); errWrite != nil {
		t.Fatal(errWrite)
	}
	before := <-input
	if before.Epoch != 0 {
		t.Fatalf("pre-loss epoch = %d, want 0", before.Epoch)
	}
	if errWrite := client.WriteMessage(websocket.PingMessage, nil); errWrite != nil {
		t.Fatal(errWrite)
	}
	<-reading
	epoch.Add(1)
	if errWrite := client.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","event_id":"recovery"}`)); errWrite != nil {
		t.Fatal(errWrite)
	}
	after := <-input
	if after.Epoch != 1 {
		t.Fatalf("post-loss recovery epoch = %d, want 1", after.Epoch)
	}
	if !cliproxyexecutor.WebsocketInputPredatesLoss(cliproxyexecutor.WithWebsocketInputEpoch(ctx, &epoch, before.Epoch)) {
		t.Fatal("previously admitted input must remain stale")
	}
	if cliproxyexecutor.WebsocketInputPredatesLoss(cliproxyexecutor.WithWebsocketInputEpoch(ctx, &epoch, after.Epoch)) {
		t.Fatal("fresh recovery input must not be stale")
	}
}
