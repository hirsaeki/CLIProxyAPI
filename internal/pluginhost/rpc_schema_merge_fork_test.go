package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
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

// Model-only filtering must survive registration alongside the newer body controls.
func TestForkRPCModelIdentifiersCoexistWithBodyPatchSchemaGate(t *testing.T) {
	for _, version := range []uint32{pluginabi.SchemaVersionRequestInterceptorOmitBody, pluginabi.SchemaVersionRequestInterceptorBodyPatch} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			source := validTestPlugin("combined-body-capabilities")
			source.Capabilities.ModelProvider = modelProviderFunc{}
			source.Capabilities.ModelProviderIdentifiers = []string{"vertex", "custom-vertex"}
			source.Capabilities.FrontendAuthProvider = frontendAuthProviderFunc{identifier: "omit-auth"}
			source.Capabilities.FrontendAuthOmitBody = true
			source.Capabilities.RequestInterceptor = &phaseRecordingInterceptor{}
			source.Capabilities.RequestInterceptorOmitBodyBefore = true
			source.Capabilities.RequestInterceptorOmitBodyAfter = true
			source.Capabilities.RequestInterceptorPatchBodyBefore = true
			source.Capabilities.RequestInterceptorPatchBodyAfter = true
			encoded := rpcCapabilitiesFromPlugin(source)
			raw, errMarshal := json.Marshal(encoded)
			if errMarshal != nil {
				t.Fatal(errMarshal)
			}
			var wire map[string]any
			if errUnmarshal := json.Unmarshal(raw, &wire); errUnmarshal != nil {
				t.Fatal(errUnmarshal)
			}
			for _, key := range []string{"frontend_auth_provider_omit_body", "frontend_auth_omit_body", "request_interceptor_omit_body_before", "request_interceptor_omit_body_after", "request_body_patcher_before", "request_body_patcher_after", "request_interceptor_patch_body_before", "request_interceptor_patch_body_after"} {
				if wire[key] != true {
					t.Fatalf("wire capability %s = %#v, want true", key, wire[key])
				}
			}
			if !reflect.DeepEqual(wire["model_provider_identifiers"], []any{"vertex", "custom-vertex"}) {
				t.Fatalf("wire identifiers = %#v", wire["model_provider_identifiers"])
			}
			encoded.ModelProviderIdentifiers[0] = "changed"
			if source.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
				t.Fatal("encoded identifiers alias source")
			}
			lookup := newTestSymbolLookup(&testPlugin{registerResult: source})
			lookup.schemaVersion = version
			registered, errRegister := registerRPCPlugin(context.Background(), nil, "combined-body-capabilities", lookup, pluginabi.MethodPluginRegister, nil)
			if errRegister != nil {
				t.Fatal(errRegister)
			}
			caps := registered.Capabilities
			if caps.ModelProvider == nil || !reflect.DeepEqual(caps.ModelProviderIdentifiers, []string{"vertex", "custom-vertex"}) {
				t.Fatalf("model identifiers lost: %#v", caps)
			}
			if caps.FrontendAuthProvider == nil || !caps.FrontendAuthProviderOmitBody || !caps.FrontendAuthOmitBody || caps.RequestInterceptor == nil || !caps.RequestInterceptorOmitBodyBefore || !caps.RequestInterceptorOmitBodyAfter {
				t.Fatalf("omit body capabilities lost: %#v", caps)
			}
			wantPatch := version >= pluginabi.SchemaVersionRequestInterceptorBodyPatch
			if (caps.RequestBodyPatcherBefore != nil) != wantPatch || (caps.RequestBodyPatcherAfter != nil) != wantPatch || caps.RequestInterceptorPatchBodyBefore != wantPatch || caps.RequestInterceptorPatchBodyAfter != wantPatch {
				t.Fatalf("patch capabilities do not honor schema %d gate: %#v", version, caps)
			}
			if !reflect.DeepEqual(lookup.lastLifecycle.HostFeatures, []string{pluginabi.HostFeatureModelProviderNativeCandidates}) {
				t.Fatalf("host features lost: %#v", lookup.lastLifecycle.HostFeatures)
			}
			caps.ModelProviderIdentifiers[0] = "client-mutated"
			if source.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
				t.Fatal("registered identifiers alias source")
			}
		})
	}
}
