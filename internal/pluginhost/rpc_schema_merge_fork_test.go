package pluginhost

import (
	"context"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Keep fork model discovery and upstream schema-v7 interception on one plugin.
func TestForkRPCModelIdentifiersCoexistWithSchema7OmitBody(t *testing.T) {
	source := validTestPlugin("combined-capabilities")
	source.Capabilities.ModelProvider = modelProviderFunc{}
	source.Capabilities.ModelProviderIdentifiers = []string{"vertex", "custom-vertex"}
	source.Capabilities.RequestInterceptor = &phaseRecordingInterceptor{}
	source.Capabilities.RequestInterceptorOmitBodyBefore = true
	source.Capabilities.RequestInterceptorOmitBodyAfter = true
	lookup := newTestSymbolLookup(&testPlugin{registerResult: source})
	lookup.schemaVersion = pluginabi.SchemaVersionRequestInterceptorOmitBody
	registered, errRegister := registerRPCPlugin(context.Background(), nil, "combined-capabilities", lookup, pluginabi.MethodPluginRegister, nil)
	if errRegister != nil {
		t.Fatal(errRegister)
	}
	caps := registered.Capabilities
	if caps.ModelProvider == nil || !reflect.DeepEqual(caps.ModelProviderIdentifiers, []string{"vertex", "custom-vertex"}) {
		t.Fatalf("model provider capabilities lost: %#v", caps)
	}
	if caps.RequestInterceptor == nil || !caps.RequestInterceptorOmitBodyBefore || !caps.RequestInterceptorOmitBodyAfter {
		t.Fatalf("omit-body capabilities lost: %#v", caps)
	}
	if !reflect.DeepEqual(lookup.lastLifecycle.HostFeatures, []string{pluginabi.HostFeatureModelProviderNativeCandidates}) {
		t.Fatalf("host features lost: %#v", lookup.lastLifecycle.HostFeatures)
	}
	// Exported RPC capability identifiers must not alias the plugin's slice.
	encoded := rpcCapabilitiesFromPlugin(pluginapi.Plugin{Capabilities: source.Capabilities})
	encoded.ModelProviderIdentifiers[0] = "changed"
	if source.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
		t.Fatal("RPC identifiers alias plugin capabilities")
	}
}
