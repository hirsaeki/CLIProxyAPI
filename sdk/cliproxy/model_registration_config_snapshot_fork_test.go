package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestPluginModelRegistrationUsesCapturedConfigFork(t *testing.T) {
	for _, provider := range []string{"custom-provider", "vertex", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			native := []*ModelInfo{{ID: "custom-model", DisplayName: "Custom model", ContextLength: 1234}, {ID: "excluded-model"}}
			switch provider {
			case "vertex":
				native = registry.GetGeminiVertexModels()
			case "antigravity":
				native = registry.GetAntigravityModels()
				resetAntigravityCapabilityCache()
				t.Cleanup(resetAntigravityCapabilityCache)
			}
			if len(native) < 2 {
				t.Fatalf("%s catalog has %d models, want at least 2", provider, len(native))
			}
			captured := &config.Config{
				OAuthExcludedModels: map[string][]string{provider: {native[1].ID}},
				OAuthModelAlias: map[string][]config.OAuthModelAlias{
					provider: {{Name: native[0].ID, Alias: "captured-alias"}},
				},
				OAuthSettings: map[string][]config.OAuthModelSetting{
					provider: {{Name: "captured-alias", MaxContextLength: 54321}},
				},
			}
			captured.ForceModelPrefix = true
			current := &config.Config{
				OAuthExcludedModels: map[string][]string{provider: {native[0].ID}},
				OAuthModelAlias: map[string][]config.OAuthModelAlias{
					provider: {{Name: native[0].ID, Alias: "current-alias"}},
				},
			}
			svc := &Service{cfg: current, pluginHost: pluginhost.New()}
			auth := &coreauth.Auth{ID: "snapshot-fork-" + provider, Provider: provider, Prefix: "tenant"}
			if provider == "antigravity" {
				auth = antigravityTestAuth(auth.ID, "https://unused.invalid")
				auth.Prefix = "tenant"
				hints := antigravityModelCapabilityHints{
					ModelIDs:          map[string]struct{}{native[0].ID: {}, native[1].ID: {}},
					WebSearchModelIDs: map[string]struct{}{native[0].ID: {}},
					revision:          1,
				}
				antigravityCapabilityMu.Lock()
				antigravityCapabilityCache[svc.antigravityCapabilityKey(auth)] = antigravityCapabilityCacheEntry{
					hints: hints, expiresAt: antigravityNowFunc().Add(time.Hour),
				}
				antigravityCapabilityMu.Unlock()
			}
			reg := GlobalModelRegistry()
			reg.UnregisterClient(auth.ID)
			t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
			originalResolver := resolvePluginModelsForAuth
			t.Cleanup(func() { resolvePluginModelsForAuth = originalResolver })
			calls := 0
			resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, a *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
				calls++
				if provider == "custom-provider" {
					if len(candidates) != 0 {
						t.Fatalf("plugin-only candidates = %#v, want none", candidates)
					}
				} else {
					var selected *ModelInfo
					for _, candidate := range candidates {
						if candidate.ID == native[1].ID {
							t.Fatal("captured exclusion was not applied before plugin discovery")
						}
						if candidate.ID == native[0].ID {
							selected = candidate
						}
					}
					if selected == nil || selected.DisplayName != native[0].DisplayName || selected.ContextLength != native[0].ContextLength {
						t.Fatalf("raw candidate or metadata was lost: %#v", candidates)
					}
					if provider == "antigravity" && !selected.SupportsWebSearch {
						t.Fatal("cached fetched model capability was lost")
					}
				}
				// A plugin can return excluded models, so the captured policy must
				// also be applied to its result before aliases, settings and prefixes.
				return pluginhost.AuthModelResult{Provider: provider, Models: native[:2], Handled: true}
			}

			svc.registerModelsForAuthWithConfigAndCache(t.Context(), captured, auth, nil)
			if calls != 1 {
				t.Fatalf("plugin discovery calls = %d, want 1", calls)
			}
			models := reg.GetModelsForClient(auth.ID)
			if len(models) != 1 || models[0].ID != "tenant/captured-alias" {
				t.Fatalf("registered models = %#v, want captured exclusion, alias and forced prefix", models)
			}
			if models[0].ContextLength != 54321 || models[0].MaxContextLength != 54321 {
				t.Fatalf("registered settings = %#v, want captured context limit", models[0])
			}
		})
	}
}

func TestPluginNativeCandidatesUseCapturedOverridesFork(t *testing.T) {
	captured := &config.Config{
		VertexCompatAPIKey: []config.VertexCompatKey{{
			APIKey: "fixture-key",
			Models: []config.VertexCompatModel{
				{Name: "captured-model", DisplayName: "Captured model", Thinking: &registry.ThinkingSupport{Min: 128, Max: 4096}},
				{Name: "excluded-model"},
			},
			ExcludedModels: []string{"excluded-model"},
		}},
	}
	captured.ForceModelPrefix = true
	current := &config.Config{
		VertexCompatAPIKey: []config.VertexCompatKey{{
			APIKey: "fixture-key",
			Models: []config.VertexCompatModel{{Name: "current-model"}},
		}},
	}
	svc := &Service{cfg: current, pluginHost: pluginhost.New()}
	auth := &coreauth.Auth{
		ID: "snapshot-fork-native-override", Provider: "vertex", Prefix: "tenant",
		Attributes: map[string]string{"api_key": "fixture-key"},
	}
	reg := GlobalModelRegistry()
	reg.UnregisterClient(auth.ID)
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	originalResolver := resolvePluginModelsForAuth
	t.Cleanup(func() { resolvePluginModelsForAuth = originalResolver })
	calls := 0
	resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, a *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
		calls++
		if len(candidates) != 1 || candidates[0].ID != "captured-model" {
			t.Fatalf("native candidates = %#v, want captured model override after exclusions", candidates)
		}
		if candidates[0].DisplayName != "Captured model" || candidates[0].Thinking == nil || candidates[0].Thinking.Max != 4096 {
			t.Fatalf("configured candidate metadata was lost: %#v", candidates[0])
		}
		return pluginhost.AuthModelResult{Provider: "vertex", Models: candidates, Handled: true}
	}

	svc.registerModelsForAuthWithConfigAndCache(t.Context(), captured, auth, nil)
	if calls != 1 {
		t.Fatalf("plugin discovery calls = %d, want 1", calls)
	}
	models := reg.GetModelsForClient(auth.ID)
	if len(models) != 1 || models[0].ID != "tenant/captured-model" || models[0].Thinking == nil || models[0].Thinking.Max != 4096 {
		t.Fatalf("registered models = %#v, want captured override and forced prefix", models)
	}
}
