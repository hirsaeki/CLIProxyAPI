package pluginhost

import (
	"context"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestMultiAuthModelCandidatesFork(t *testing.T) {
	for _, transport := range []string{"native", "rpc"} {
		for _, mode := range []string{"plural", "legacy-and-plural", "explicit-override", "explicit-mismatch"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				calls := 0
				source := validTestPlugin("multi-auth-models")
				source.Capabilities.AuthProviders = []pluginapi.AuthProvider{
					nil,
					fakeAuthProvider{identifier: "portal-one"},
					fakeAuthProvider{identifier: "portal-two", identifiers: []string{" VERTEX "}},
				}
				source.Capabilities.ModelProvider = modelProviderFunc{modelsForAuth: func(_ context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
					calls++
					if req.AuthProvider != "vertex" || len(req.CandidateModels) != 1 || req.CandidateModels[0].ID != "native-model" || req.CandidateModels[0].Thinking == nil {
						t.Fatalf("native candidates lost: %#v", req)
					}
					req.CandidateModels[0].Thinking.Levels[0] = "changed"
					return pluginapi.ModelResponse{Provider: "vertex", Models: req.CandidateModels}, nil
				}}
				switch mode {
				case "legacy-and-plural":
					source.Capabilities.AuthProvider = fakeAuthProvider{identifier: "legacy"}
				case "explicit-override":
					source.Capabilities.ModelProviderIdentifiers = []string{" VERTEX "}
					source.Capabilities.AuthProviders[2] = fakeAuthProvider{identifier: "portal-two"}
				case "explicit-mismatch":
					source.Capabilities.ModelProviderIdentifiers = []string{"unrelated"}
				}
				plugin := source
				if transport == "rpc" {
					lookup := newTestSymbolLookup(&testPlugin{registerResult: source})
					var errRegister error
					plugin, errRegister = registerRPCPlugin(t.Context(), nil, "multi-auth-models", lookup, pluginabi.MethodPluginRegister, nil)
					if errRegister != nil {
						t.Fatal(errRegister)
					}
					if plugin.Capabilities.AuthProvider == nil || !reflect.DeepEqual(plugin.Capabilities.ModelProviderIdentifiers, source.Capabilities.ModelProviderIdentifiers) {
						t.Fatalf("RPC capabilities lost: %#v", plugin.Capabilities)
					}
					if !reflect.DeepEqual(lookup.lastLifecycle.HostFeatures, []string{pluginabi.HostFeatureModelProviderNativeCandidates}) {
						t.Fatalf("native candidate feature lost: %#v", lookup.lastLifecycle.HostFeatures)
					}
				}
				host := newHostWithRecords(capabilityRecord{id: "multi-auth-models", plugin: plugin})
				for _, provider := range []string{"portal-one", "portal-two"} {
					if !host.HasAuthProvider(provider) {
						t.Fatalf("auth provider %q missing", provider)
					}
				}
				if got := host.HasAuthProvider("vertex"); got != (mode != "explicit-override") {
					t.Fatalf("vertex auth ownership = %v", got)
				}
				want := mode != "explicit-mismatch"
				if got := host.HasAuthModelProvider(" VERTEX "); got != want {
					t.Fatalf("model ownership = %v, want %v", got, want)
				}
				if calls != 0 {
					t.Fatal("ownership lookup fetched models")
				}
				if mode == "explicit-override" && host.HasAuthModelProvider("portal-one") {
					t.Fatal("explicit model identifiers must override auth identifiers")
				}
				candidate := &registry.ModelInfo{ID: "native-model", Thinking: &registry.ThinkingSupport{Levels: []string{"high"}}}
				result := host.ModelsForAuth(t.Context(), &coreauth.Auth{ID: "account", Provider: "vertex"}, []*registry.ModelInfo{candidate})
				if result.Handled != want || result.Err != nil {
					t.Fatalf("model dispatch = %#v, want handled %v", result, want)
				}
				if want && (calls != 1 || len(result.Models) != 1 || result.Models[0].ID != candidate.ID) {
					t.Fatalf("discovery result = %#v, calls = %d", result, calls)
				}
				if !want && calls != 0 {
					t.Fatal("ineligible plugin fetched models")
				}
				if candidate.Thinking.Levels[0] != "high" {
					t.Fatal("plugin mutated caller-owned candidate")
				}
			})
		}
	}
}

func TestRPCMultiAuthCapabilityDetectionFork(t *testing.T) {
	for _, tt := range []struct {
		name string
		auth []pluginapi.AuthProvider
		want bool
	}{
		{name: "absent"},
		{name: "nil-only", auth: []pluginapi.AuthProvider{nil}},
		{name: "plural", auth: []pluginapi.AuthProvider{nil, fakeAuthProvider{identifier: "vertex"}}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
				AuthProviders:            tt.auth,
				ModelProvider:            modelProviderFunc{},
				ModelProviderIdentifiers: []string{"vertex"},
			}}
			caps := rpcCapabilitiesFromPlugin(source)
			if caps.AuthProvider != tt.want || !caps.ModelProvider || !reflect.DeepEqual(caps.ModelProviderIdentifiers, []string{"vertex"}) {
				t.Fatalf("capabilities = %#v, want auth provider %v", caps, tt.want)
			}
			caps.ModelProviderIdentifiers[0] = "changed"
			if source.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
				t.Fatal("RPC identifiers alias source")
			}
		})
	}
}

func TestMultiAuthModelProviderFallbackFork(t *testing.T) {
	for _, fallback := range []string{"static", "executor"} {
		t.Run(fallback, func(t *testing.T) {
			caps := pluginapi.Capabilities{
				AuthProviders: []pluginapi.AuthProvider{fakeAuthProvider{identifier: "login-portal"}},
				ModelProvider: modelProviderFunc{},
			}
			if fallback == "executor" {
				caps.Executor = &fakeExecutor{identifier: "vertex"}
			}
			host := newHostWithRecords(capabilityRecord{id: "fallback", plugin: pluginapi.Plugin{Capabilities: caps}})
			if fallback == "static" {
				host.modelProviders["fallback"] = "vertex"
			}
			if !host.HasAuthModelProvider("vertex") || host.HasAuthModelProvider("unrelated") {
				t.Fatal("auth provider mismatch must retain static/executor fallback")
			}
		})
	}
}
