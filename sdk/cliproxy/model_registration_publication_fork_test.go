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

// The auth inputs deliberately stay unchanged while a newer registration is
// published. Only the entry-captured registry epoch can reject the old result.
func TestPluginRegistrationRetainsEntryPublicationFenceFork(t *testing.T) {
	for _, provider := range []string{"custom-provider", "vertex", "antigravity"} {
		for _, resultKind := range []string{"models", "empty", "unhandled"} {
			if resultKind == "unhandled" && provider != "antigravity" {
				continue
			}
			t.Run(provider+"/"+resultKind, func(t *testing.T) {
				svc, auth := publicationFixtureFork(t, provider, nil)
				if resultKind == "unhandled" {
					// Isolate this synchronous publication from the independently
					// admitted background refresh that may publish fresh hints.
					lifetime, cancel := context.WithCancel(t.Context())
					cancel()
					svc.antigravityContext = lifetime
				}
				reg := GlobalModelRegistry()
				var newerEpoch uint64
				interleaved := false
				auth.Runtime = &registrationInterleaveRuntime{onRead: func() {
					if interleaved {
						t.Fatal("runtime read more than once")
					}
					interleaved = true
					reg.RegisterClient(auth.ID, provider, []*ModelInfo{{ID: "newer-model"}})
					newerEpoch = reg.ClientRegistrationEpoch(auth.ID)
				}}
				calls := 0
				setPublicationResolverFork(t, func(_ *pluginhost.Host, _ context.Context, _ *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
					calls++
					checkPublicationCandidatesFork(t, provider, candidates)
					result := pluginhost.AuthModelResult{Provider: provider, Handled: resultKind != "unhandled"}
					if resultKind == "models" {
						result.Models = []*ModelInfo{{ID: "older-model"}}
					}
					return result
				})

				svc.registerModelsForAuth(t.Context(), auth)
				if !interleaved || calls != 1 {
					t.Fatalf("runtime interleaved=%v, plugin calls=%d; want true, 1", interleaved, calls)
				}
				assertNewerPublicationFork(t, auth.ID, newerEpoch)
			})
		}
	}
}

// An old plugin result with a zero registration epoch must not adopt an auth
// identity that was removed and re-registered while discovery was in flight.
func TestPluginRegistrationRejectsSupersededAuthUpdateFork(t *testing.T) {
	for _, provider := range []string{"custom-provider", "vertex", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			svc, auth := publicationFixtureFork(t, provider, nil)
			reg := GlobalModelRegistry()
			var freshEpoch, newerEpoch uint64
			calls := 0
			setPublicationResolverFork(t, func(_ *pluginhost.Host, ctx context.Context, a *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
				calls++
				checkPublicationCandidatesFork(t, provider, candidates)
				svc.applyCoreAuthRemoval(ctx, a.ID)
				fresh := a.Clone()
				fresh.Prefix = "fresh"
				fresh.RegistrationEpoch = 0
				registered, err := svc.coreManager.Register(ctx, fresh)
				if err != nil {
					t.Fatal(err)
				}
				freshEpoch = registered.RegistrationEpoch
				reg.RegisterClient(a.ID, provider, []*ModelInfo{{ID: "newer-model"}})
				newerEpoch = reg.ClientRegistrationEpoch(a.ID)
				stale := a.Clone()
				stale.RegistrationEpoch = 0
				return pluginhost.AuthModelResult{Provider: provider, Auth: stale, Models: []*ModelInfo{{ID: "older-model"}}, Handled: true}
			})

			svc.registerModelsForAuth(t.Context(), auth)
			if calls != 1 {
				t.Fatalf("plugin calls=%d, want 1", calls)
			}
			current, ok := svc.coreManager.GetByID(auth.ID)
			if !ok || current.Prefix != "fresh" || current.RegistrationEpoch != freshEpoch {
				t.Fatalf("stale plugin result overwrote fresh auth: %#v", current)
			}
			assertNewerPublicationFork(t, auth.ID, newerEpoch)
		})
	}
}

// A valid plugin auth update may run a hook that publishes newer models before
// UpdateIfRegistrationCurrent returns. Refreshing auth inputs must not refresh
// the original registry epoch, for either model publication or unregistration.
func TestPluginRegistrationCommittedAuthRetainsEpochFork(t *testing.T) {
	for _, provider := range []string{"custom-provider", "vertex", "antigravity"} {
		for _, empty := range []bool{false, true} {
			resultKind := "models"
			if empty {
				resultKind = "empty"
			}
			t.Run(provider+"/"+resultKind, func(t *testing.T) {
				reg := GlobalModelRegistry()
				var newerEpoch uint64
				updates := 0
				hook := &publicationHookFork{onUpdate: func(auth *coreauth.Auth) {
					updates++
					reg.RegisterClient(auth.ID, provider, []*ModelInfo{{ID: "newer-model"}})
					newerEpoch = reg.ClientRegistrationEpoch(auth.ID)
				}}
				svc, auth := publicationFixtureFork(t, provider, hook)
				setPublicationResolverFork(t, func(_ *pluginhost.Host, _ context.Context, a *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
					checkPublicationCandidatesFork(t, provider, candidates)
					updated := a.Clone()
					updated.Prefix = "plugin-updated"
					result := pluginhost.AuthModelResult{Provider: provider, Auth: updated, Handled: true}
					if !empty {
						result.Models = []*ModelInfo{{ID: "older-model"}}
					}
					return result
				})

				svc.registerModelsForAuth(t.Context(), auth)
				current, ok := svc.coreManager.GetByID(auth.ID)
				if !ok || current.Prefix != "plugin-updated" || updates != 1 {
					t.Fatalf("valid plugin update did not commit exactly once: auth=%#v, updates=%d", current, updates)
				}
				assertNewerPublicationFork(t, auth.ID, newerEpoch)
			})
		}
	}
}

func publicationFixtureFork(t *testing.T, provider string, hook coreauth.Hook) (*Service, *coreauth.Auth) {
	t.Helper()
	svc := &Service{cfg: &config.Config{}, pluginHost: pluginhost.New(), coreManager: coreauth.NewManager(nil, &coreauth.RoundRobinSelector{}, hook)}
	svc.cfg.ForceModelPrefix = true
	auth := &coreauth.Auth{ID: t.Name(), Provider: provider, Prefix: "old", Status: coreauth.StatusActive}
	if provider == "antigravity" {
		resetAntigravityCapabilityCache()
		t.Cleanup(resetAntigravityCapabilityCache)
		auth = antigravityTestAuth(auth.ID, "https://unused.invalid")
		auth.Prefix = "old"
	}
	registered, err := svc.coreManager.Register(t.Context(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if provider == "antigravity" {
		native := registry.GetAntigravityModels()
		if len(native) == 0 {
			t.Fatal("Antigravity model catalog is empty")
		}
		hints := antigravityModelCapabilityHints{ModelIDs: map[string]struct{}{native[0].ID: {}}, revision: 1}
		antigravityCapabilityMu.Lock()
		antigravityCapabilityCache[svc.antigravityCapabilityKey(registered)] = antigravityCapabilityCacheEntry{hints: hints, expiresAt: antigravityNowFunc().Add(time.Hour)}
		antigravityCapabilityMu.Unlock()
	}
	reg := GlobalModelRegistry()
	reg.UnregisterClient(auth.ID)
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	return svc, registered
}

func setPublicationResolverFork(t *testing.T, resolver func(*pluginhost.Host, context.Context, *coreauth.Auth, []*ModelInfo) pluginhost.AuthModelResult) {
	t.Helper()
	original := resolvePluginModelsForAuth
	t.Cleanup(func() { resolvePluginModelsForAuth = original })
	resolvePluginModelsForAuth = resolver
}

func checkPublicationCandidatesFork(t *testing.T, provider string, candidates []*ModelInfo) {
	t.Helper()
	if provider == "custom-provider" && len(candidates) != 0 {
		t.Fatalf("plugin-only candidates=%#v, want none", candidates)
	}
	if provider != "custom-provider" && len(candidates) == 0 {
		t.Fatal("native candidates were lost")
	}
}

func assertNewerPublicationFork(t *testing.T, authID string, epoch uint64) {
	t.Helper()
	reg := GlobalModelRegistry()
	models := reg.GetModelsForClient(authID)
	if len(models) != 1 || models[0].ID != "newer-model" || reg.ClientRegistrationEpoch(authID) != epoch {
		t.Fatalf("stale calculation changed newer publication: models=%#v, epoch=%d, want %d", models, reg.ClientRegistrationEpoch(authID), epoch)
	}
}

type publicationHookFork struct{ onUpdate func(*coreauth.Auth) }

func (*publicationHookFork) OnAuthRegistered(context.Context, *coreauth.Auth) {}
func (h *publicationHookFork) OnAuthUpdated(_ context.Context, auth *coreauth.Auth) {
	h.onUpdate(auth)
}
func (*publicationHookFork) OnResult(context.Context, coreauth.Result) {}
