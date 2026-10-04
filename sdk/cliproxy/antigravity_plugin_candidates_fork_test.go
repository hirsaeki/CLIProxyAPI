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

func TestAntigravityCachedRegistrationPassesNativeCandidatesToPlugin(t *testing.T) {
	for _, name := range []string{"fresh", "expired", "empty"} {
		t.Run(name, func(t *testing.T) {
			resetAntigravityCapabilityCache()
			t.Cleanup(resetAntigravityCapabilityCache)
			native := registry.GetAntigravityModels()
			if len(native) < 3 {
				t.Fatalf("Antigravity catalog has %d models, want at least 3", len(native))
			}
			cfg := &config.Config{
				OAuthModelAlias: map[string][]config.OAuthModelAlias{
					"antigravity": {{Name: native[0].ID, Alias: "visible-model"}},
				},
			}
			cfg.ForceModelPrefix = true
			svc := &Service{cfg: cfg, coreManager: coreauth.NewManager(nil, nil, nil), pluginHost: pluginhost.New()}
			auth := antigravityTestAuth("cached-plugin-"+name, "https://unused.invalid")
			auth.Prefix = "tenant"
			auth.Attributes["excluded_models"] = native[1].ID
			auth, errRegister := svc.coreManager.Register(t.Context(), auth)
			if errRegister != nil {
				t.Fatal(errRegister)
			}
			reg := GlobalModelRegistry()
			reg.RegisterClient(auth.ID, "antigravity", native[:2])
			t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
			hints := antigravityModelCapabilityHints{
				ModelIDs:          map[string]struct{}{native[0].ID: {}, native[1].ID: {}},
				WebSearchModelIDs: map[string]struct{}{native[0].ID: {}},
				revision:          1,
			}
			if name == "empty" {
				hints.ModelIDs = map[string]struct{}{}
			}
			expiresAt := antigravityNowFunc().Add(time.Hour)
			if name == "expired" {
				expiresAt = time.Time{}
			}
			antigravityCapabilityMu.Lock()
			antigravityCapabilityCache[svc.antigravityCapabilityKey(auth)] = antigravityCapabilityCacheEntry{hints: hints, expiresAt: expiresAt}
			antigravityCapabilityMu.Unlock()

			originalResolver, originalOwnership := resolvePluginModelsForAuth, pluginHostHasAuthModelProvider
			t.Cleanup(func() {
				resolvePluginModelsForAuth = originalResolver
				pluginHostHasAuthModelProvider = originalOwnership
			})
			pluginHostHasAuthModelProvider = func(host *pluginhost.Host, provider string) bool {
				return host == svc.pluginHost && provider == "antigravity"
			}
			calls := 0
			resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, a *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
				calls++
				if host != svc.pluginHost || a.ID != auth.ID {
					t.Fatal("plugin discovery received the wrong host or auth")
				}
				if name == "empty" {
					if len(candidates) != 0 {
						t.Fatalf("empty catalog candidates = %#v", candidates)
					}
				} else {
					if len(candidates) != 1 || candidates[0].ID != native[0].ID {
						t.Fatalf("candidates = %#v, want the entitled, non-excluded raw model", candidates)
					}
					if candidates[0].DisplayName != native[0].DisplayName || candidates[0].ContextLength != native[0].ContextLength || !candidates[0].SupportsWebSearch {
						t.Fatalf("candidate metadata was lost: %#v", candidates[0])
					}
				}
				return pluginhost.AuthModelResult{
					Handled:  true,
					Provider: "antigravity",
					Models:   []*ModelInfo{{ID: native[0].ID, DisplayName: "Plugin selected"}},
				}
			}

			svc.registerModelsForAuth(t.Context(), auth)
			svc.WaitAntigravityProbes()
			if calls != 1 {
				t.Fatalf("plugin discovery calls = %d, want 1 before cached native publication", calls)
			}
			models := reg.GetModelsForClient(auth.ID)
			if len(models) != 1 || models[0].ID != "tenant/visible-model" || models[0].DisplayName != "Plugin selected" {
				t.Fatalf("registered models = %#v, want the plugin result with alias and prefix applied afterward", models)
			}
			antigravityCapabilityMu.RLock()
			entry := antigravityCapabilityCache[svc.antigravityCapabilityKey(auth)]
			antigravityCapabilityMu.RUnlock()
			if entry.appliedRevision != 0 || entry.pending {
				t.Fatal("native publication or probing ran for the plugin-owned catalog")
			}
		})
	}
}

func TestAntigravityCachedRegistrationWithoutPluginPreservesRegistryState(t *testing.T) {
	resetAntigravityCapabilityCache()
	t.Cleanup(resetAntigravityCapabilityCache)
	native := registry.GetAntigravityModels()
	if len(native) == 0 {
		t.Fatal("Antigravity catalog is empty")
	}
	svc := &Service{cfg: &config.Config{}, pluginHost: pluginhost.New()}
	auth := antigravityTestAuth("cached-without-plugin", "https://unused.invalid")
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, "antigravity", native[:1])
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	reg.SetModelQuotaExceeded(auth.ID, native[0].ID)
	reg.SuspendClientModel(auth.ID, native[0].ID, "quota")
	epoch := reg.ClientRegistrationEpoch(auth.ID)
	hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{native[0].ID: {}}, revision: 1}
	key := svc.antigravityCapabilityKey(auth)
	antigravityCapabilityMu.Lock()
	antigravityCapabilityCache[key] = antigravityCapabilityCacheEntry{hints: hints, expiresAt: antigravityNowFunc().Add(time.Hour)}
	antigravityCapabilityMu.Unlock()

	svc.registerModelsForAuth(t.Context(), auth)
	svc.WaitAntigravityProbes()
	if !reg.ClientSupportsModel(auth.ID, native[0].ID) || !reg.IsModelQuotaExceededForClient(auth.ID, native[0].ID) || !reg.IsModelSuspendedForClient(auth.ID, native[0].ID) {
		t.Fatal("cached native registration lost its model, quota, or suspension state")
	}
	antigravityCapabilityMu.RLock()
	entry := antigravityCapabilityCache[key]
	antigravityCapabilityMu.RUnlock()
	if entry.appliedRevision != hints.revision || entry.appliedEpoch != reg.ClientRegistrationEpoch(auth.ID) || entry.appliedEpoch <= epoch {
		t.Fatal("cached native registration bypassed fenced publication")
	}
}
