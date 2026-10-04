package pluginhost

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestForkAuthModelProviderOwnershipMatchesExplicitIdentifiers(t *testing.T) {
	for _, mode := range []string{"explicit", "auth-override", "mismatch", "static-only", "fused", "unloaded", "no-model-provider"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			candidate := &registry.ModelInfo{ID: "native-model", DisplayName: "Native", Thinking: &registry.ThinkingSupport{Levels: []string{"high"}}}
			caps := pluginapi.Capabilities{
				ModelProviderIdentifiers: []string{"vertex", " ANTIGRAVITY "},
				ModelProvider: modelProviderFunc{
					staticModels: func(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
						t.Fatal("ownership lookup fetched static models")
						return pluginapi.ModelResponse{}, nil
					},
					modelsForAuth: func(_ context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
						calls++
						if len(req.CandidateModels) != 1 || req.CandidateModels[0].ID != candidate.ID || req.CandidateModels[0].DisplayName != candidate.DisplayName || req.CandidateModels[0].Thinking == nil {
							t.Fatalf("native candidates lost: %#v", req.CandidateModels)
						}
						req.CandidateModels[0].Thinking.Levels[0] = "mutated"
						return pluginapi.ModelResponse{Provider: "antigravity", Models: req.CandidateModels}, nil
					},
				},
			}
			switch mode {
			case "auth-override":
				caps.AuthProvider = fakeAuthProvider{identifier: "other-provider"}
			case "mismatch":
				caps.ModelProviderIdentifiers = []string{"vertex"}
				caps.AuthProvider = fakeAuthProvider{identifier: "antigravity"}
			case "static-only":
				caps.Executor = &fakeExecutor{identifier: "antigravity"}
				caps.ExecutorModelScope = pluginapi.ExecutorModelScopeStatic
			case "no-model-provider":
				caps.ModelProvider = nil
			}
			host := newHostWithRecords(capabilityRecord{id: "fork-ownership", plugin: pluginapi.Plugin{Capabilities: caps}})
			if mode == "fused" {
				host.fused["fork-ownership"] = "test"
			}
			if mode == "unloaded" {
				setHostSnapshotForTest(host, true)
			}
			want := mode == "explicit" || mode == "auth-override"
			if got := host.HasAuthModelProvider(" ANTIGRAVITY "); got != want {
				t.Fatalf("ownership = %v, want %v", got, want)
			}
			if calls != 0 || host.HasAuthModelProvider("unrelated") {
				t.Fatal("ownership lookup ran discovery or claimed an unrelated provider")
			}
			result := host.ModelsForAuth(t.Context(), &coreauth.Auth{ID: "fork-account", Provider: "antigravity"}, []*registry.ModelInfo{nil, candidate})
			if result.Handled != want {
				t.Fatalf("per-auth dispatch = %v, want %v", result.Handled, want)
			}
			if want && (calls != 1 || len(result.Models) != 1 || result.Models[0].ID != candidate.ID) {
				t.Fatalf("per-auth discovery lost models: %#v (calls=%d)", result, calls)
			}
			if !want && calls != 0 {
				t.Fatal("ineligible provider ran discovery")
			}
			if candidate.Thinking.Levels[0] != "high" {
				t.Fatal("plugin mutated the native candidate")
			}
		})
	}
}
