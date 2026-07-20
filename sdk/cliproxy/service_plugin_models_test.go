package cliproxy

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRegisterModelsForAuthPassesNativeCandidatesToPlugin(t *testing.T) {
	vertexModels := registry.GetGeminiVertexModels()
	if len(vertexModels) < 2 {
		t.Fatalf("Vertex model fixture has %d models, want at least 2", len(vertexModels))
	}
	authID := "test-vertex-plugin-candidates"
	GlobalModelRegistry().UnregisterClient(authID)
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(authID) })

	originalResolver := resolvePluginModelsForAuth
	t.Cleanup(func() { resolvePluginModelsForAuth = originalResolver })
	var gotCandidates []*ModelInfo
	resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, auth *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
		gotCandidates = candidates
		return pluginhost.AuthModelResult{
			Provider: "vertex",
			Models:   candidates[:1],
			Handled:  true,
		}
	}

	service := &Service{pluginHost: pluginhost.New()}
	service.registerModelsForAuth(context.Background(), &coreauth.Auth{
		ID:       authID,
		Provider: "vertex",
		Attributes: map[string]string{
			"excluded_models": vertexModels[1].ID,
		},
	})

	if len(gotCandidates) != len(vertexModels)-1 {
		t.Fatalf("plugin candidates = %d, want %d after exclusions", len(gotCandidates), len(vertexModels)-1)
	}
	for _, model := range gotCandidates {
		if model != nil && model.ID == vertexModels[1].ID {
			t.Fatalf("excluded model %q was passed to plugin", model.ID)
		}
	}
	registered := registry.GetGlobalRegistry().GetModelsForClient(authID)
	if len(registered) != 1 || registered[0].ID != gotCandidates[0].ID {
		t.Fatalf("registered models = %#v, want plugin-filtered candidate", registered)
	}
	if registered[0].DisplayName != gotCandidates[0].DisplayName || registered[0].Thinking == nil {
		t.Fatalf("registered model metadata was not preserved: %#v", registered[0])
	}
}

func TestRegisterModelsForAuthKeepsPreNativePluginDiscoveryForCustomProvider(t *testing.T) {
	authID := "test-custom-plugin-models"
	GlobalModelRegistry().UnregisterClient(authID)
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(authID) })

	originalResolver := resolvePluginModelsForAuth
	t.Cleanup(func() { resolvePluginModelsForAuth = originalResolver })
	resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, auth *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
		if len(candidates) != 0 {
			t.Fatalf("custom provider candidates = %#v, want none", candidates)
		}
		return pluginhost.AuthModelResult{
			Provider: "custom-provider",
			Models:   []*ModelInfo{{ID: "custom-model", DisplayName: "Custom Model"}},
			Handled:  true,
		}
	}

	service := &Service{pluginHost: pluginhost.New()}
	service.registerModelsForAuth(context.Background(), &coreauth.Auth{ID: authID, Provider: "custom-provider"})

	registered := registry.GetGlobalRegistry().GetModelsForClient(authID)
	if len(registered) != 1 || registered[0].ID != "custom-model" {
		t.Fatalf("registered models = %#v, want custom plugin model", registered)
	}
}
