package pluginhost

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRPCCapabilitiesIncludeModelProviderIdentifiers(t *testing.T) {
	plugin := pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		ModelProvider:            modelProviderFunc{},
		ModelProviderIdentifiers: []string{"vertex", "custom-vertex"},
	}}

	caps := rpcCapabilitiesFromPlugin(plugin)
	if !caps.ModelProvider || !reflect.DeepEqual(caps.ModelProviderIdentifiers, []string{"vertex", "custom-vertex"}) {
		t.Fatalf("RPC capabilities = %#v", caps)
	}
	raw, errMarshal := json.Marshal(caps)
	if errMarshal != nil {
		t.Fatalf("Marshal() error = %v", errMarshal)
	}
	var decoded map[string]any
	if errUnmarshal := json.Unmarshal(raw, &decoded); errUnmarshal != nil {
		t.Fatalf("Unmarshal() error = %v", errUnmarshal)
	}
	identifiers, okIdentifiers := decoded["model_provider_identifiers"].([]any)
	if !okIdentifiers || len(identifiers) != 2 || identifiers[0] != "vertex" || identifiers[1] != "custom-vertex" {
		t.Fatalf("model_provider_identifiers = %#v", decoded["model_provider_identifiers"])
	}
	caps.ModelProviderIdentifiers[0] = "mutated"
	if plugin.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
		t.Fatalf("RPC capability conversion mutated plugin identifiers: %#v", plugin.Capabilities.ModelProviderIdentifiers)
	}
}

func TestRPCModelProviderPreservesIdentifiersAndCandidateModels(t *testing.T) {
	var gotRequest pluginapi.AuthModelRequest
	registeredSource := validTestPlugin("vertex-model-filter")
	registeredSource.Capabilities.ModelProviderIdentifiers = []string{"vertex"}
	registeredSource.Capabilities.ModelProvider = modelProviderFunc{modelsForAuth: func(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
		gotRequest = req
		return pluginapi.ModelResponse{Provider: "vertex", Models: req.CandidateModels[:1]}, nil
	}}
	lookup := newTestSymbolLookup(&testPlugin{registerResult: registeredSource})

	registered, errRegister := registerRPCPlugin(context.Background(), nil, "vertex-model-filter", lookup, pluginabi.MethodPluginRegister, nil)
	if errRegister != nil {
		t.Fatalf("registerRPCPlugin() error = %v", errRegister)
	}
	if !reflect.DeepEqual(registered.Capabilities.ModelProviderIdentifiers, []string{"vertex"}) {
		t.Fatalf("registered identifiers = %#v", registered.Capabilities.ModelProviderIdentifiers)
	}
	response, errModels := registered.Capabilities.ModelProvider.ModelsForAuth(context.Background(), pluginapi.AuthModelRequest{
		AuthID:       "vertex-auth",
		AuthProvider: "vertex",
		CandidateModels: []pluginapi.ModelInfo{
			{ID: "gemini-supported", DisplayName: "Supported"},
			{ID: "gemini-unsupported", DisplayName: "Unsupported"},
		},
	})
	if errModels != nil {
		t.Fatalf("ModelsForAuth() error = %v", errModels)
	}
	if len(gotRequest.CandidateModels) != 2 || gotRequest.CandidateModels[1].ID != "gemini-unsupported" {
		t.Fatalf("RPC candidate models = %#v", gotRequest.CandidateModels)
	}
	if len(response.Models) != 1 || response.Models[0].ID != "gemini-supported" {
		t.Fatalf("RPC model response = %#v", response)
	}
}

func TestRegisterRPCPluginAdvertisesNativeModelCandidates(t *testing.T) {
	lookup := newTestSymbolLookup(&testPlugin{registerResult: validTestPlugin("native-candidates")})
	if _, errRegister := registerRPCPlugin(context.Background(), nil, "native-candidates", lookup, pluginabi.MethodPluginRegister, nil); errRegister != nil {
		t.Fatalf("registerRPCPlugin() error = %v", errRegister)
	}
	if !reflect.DeepEqual(lookup.lastLifecycle.HostFeatures, []string{pluginabi.HostFeatureModelProviderNativeCandidates}) {
		t.Fatalf("lifecycle host_features = %#v", lookup.lastLifecycle.HostFeatures)
	}
}
