package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRPCLifecycleCapabilitiesCoexistFork(t *testing.T) {
	for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
		for _, schema := range []uint32{1, pluginabi.SchemaVersion} {
			t.Run(fmt.Sprintf("%s/schema-%d", method, schema), func(t *testing.T) {
				source := validTestPlugin("combined-features")
				lookup := newTestSymbolLookup(&testPlugin{registerResult: source, reconfigureResult: source})
				lookup.schemaVersion = schema
				var wire map[string]json.RawMessage
				client := &lifecycleTestClient{call: func(ctx context.Context, calledMethod string, request []byte) ([]byte, error) {
					if calledMethod == method {
						if err := json.Unmarshal(request, &wire); err != nil {
							return nil, err
						}
					}
					return lookup.Call(ctx, calledMethod, request)
				}}
				registered, err := registerRPCPlugin(t.Context(), nil, "combined-features", client, method, []byte("mode: test"))
				if err != nil {
					t.Fatal(err)
				}
				if string(wire["multi_auth_providers"]) != "true" {
					t.Fatalf("multi_auth_providers = %s, want true", wire["multi_auth_providers"])
				}
				var features []string
				if err := json.Unmarshal(wire["host_features"], &features); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(features, []string{pluginabi.HostFeatureModelProviderNativeCandidates}) {
					t.Fatalf("host_features = %#v", features)
				}
				if lookup.lastLifecycle.SchemaVersion != pluginabi.SchemaVersion || registered.SchemaVersion != schema || string(lookup.lastLifecycle.ConfigYAML) != "mode: test" {
					t.Fatalf("lifecycle = %#v, negotiated schema = %d", lookup.lastLifecycle, registered.SchemaVersion)
				}
			})
		}
	}
}

func TestRPCAuthEntriesNativeCandidatesFork(t *testing.T) {
	var modelCalls int
	client := &lifecycleTestClient{call: func(_ context.Context, method string, request []byte) ([]byte, error) {
		switch method {
		case pluginabi.MethodPluginRegister:
			return marshalRPCResult(rpcRegistration{
				SchemaVersion: pluginabi.SchemaVersion,
				Metadata:      pluginapi.Metadata{Name: "Portal Plugin", Logo: "https://example.invalid/default.svg"},
				Capabilities: rpcCapabilities{
					AuthProvider: true, ModelProvider: true,
					ModelProviderIdentifiers: []string{"vertex"},
				},
			})
		case pluginabi.MethodAuthIdentifier:
			return []byte(`{"ok":true,"result":{"entries":[{"identifier":" Portal-One ","name":"Portal One","logo":"https://example.invalid/one.svg"},{"identifier":"portal-two"}]}}`), nil
		case pluginabi.MethodModelForAuth:
			modelCalls++
			var req pluginapi.AuthModelRequest
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
			if req.AuthProvider != "vertex" || len(req.CandidateModels) != 1 || req.CandidateModels[0].ID != "native-model" {
				t.Fatalf("native candidate request = %#v", req)
			}
			return marshalRPCResult(pluginapi.ModelResponse{Provider: "vertex", Models: req.CandidateModels})
		default:
			return nil, fmt.Errorf("unexpected method %s", method)
		}
	}}
	plugin, err := registerRPCPlugin(t.Context(), nil, "combined-entries", client, pluginabi.MethodPluginRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	host := newHostWithRecords(capabilityRecord{id: "combined-entries", meta: plugin.Metadata, plugin: plugin})
	infos := host.RegisteredPlugins()
	wantEntries := []pluginapi.AuthProviderEntry{
		{Identifier: "portal-one", Name: "Portal One", Logo: "https://example.invalid/one.svg"},
		{Identifier: "portal-two", Name: "Portal Plugin", Logo: "https://example.invalid/default.svg"},
	}
	if len(infos) != 1 || !reflect.DeepEqual(infos[0].OAuthProviderEntries, wantEntries) {
		t.Fatalf("registered metadata = %#v", infos)
	}
	if !host.HasAuthProvider("portal-one") || !host.HasAuthProvider("portal-two") || !host.HasAuthModelProvider("vertex") || host.HasAuthModelProvider("portal-one") {
		t.Fatal("OAuth entries must coexist with authoritative model-provider identifiers")
	}
	result := host.ModelsForAuth(t.Context(), &coreauth.Auth{ID: "auth", Provider: "vertex"}, []*registry.ModelInfo{{ID: "native-model"}})
	if !result.Handled || result.Err != nil || modelCalls != 1 || len(result.Models) != 1 || result.Models[0].ID != "native-model" {
		t.Fatalf("model result = %#v, calls = %d", result, modelCalls)
	}
}
