package pluginhost

import (
	"context"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type callbackCloseOnErrContextFork struct {
	context.Context
	closeCallback func()
}

func (ctx *callbackCloseOnErrContextFork) Err() error {
	ctx.closeCallback()
	return ctx.Context.Err()
}

func TestHostContextDoneReportsClosureAfterLookupFork(t *testing.T) {
	h := New()
	inst := &hostCallbackInstance{}
	caller := withHostCallbackIdentity(context.Background(), "test-plugin", inst)
	target := &callbackCloseOnErrContextFork{Context: context.Background()}
	id, closeCallback := h.openCallbackContextForPluginInstance(target, "test-plugin", inst)
	defer closeCallback()

	// Close the callback when Err is read, after the registry lookup but
	// without canceling the underlying request or relying on timing.
	target.closeCallback = closeCallback
	req, errMarshal := json.Marshal(pluginapi.HostContextDoneRequest{HostCallbackID: id})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	raw, errCall := h.callFromPlugin(caller, pluginabi.MethodHostContextDone, req)
	if errCall != nil {
		t.Fatal(errCall)
	}
	res, errDecode := decodeRPCEnvelope[pluginapi.HostContextDoneResponse](raw)
	if errDecode != nil {
		t.Fatal(errDecode)
	}
	if !res.Done || res.Reason != "closed" {
		t.Fatalf("host.context.done after closure = %#v; want done=true and reason=closed", res)
	}
}
