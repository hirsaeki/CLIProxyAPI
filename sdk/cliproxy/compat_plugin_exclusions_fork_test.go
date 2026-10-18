package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func TestCompatStaticPluginExclusionsFork(t *testing.T) {
	const name = "static-compat-fork"
	providerKey := util.OpenAICompatibleProviderKey(name)
	for _, tc := range []struct {
		name       string
		configured bool
		native     bool
		cached     bool
	}{
		{name: "configured models", configured: true, native: true},
		{name: "configured empty", configured: true},
		{name: "cached models", configured: true, native: true, cached: true},
		{name: "cached empty", configured: true, cached: true},
		{name: "no matching config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, ownRules := range []bool{false, true} {
				t.Run(map[bool]string{false: "global", true: "credential override"}[ownRules], func(t *testing.T) {
					cfg := &config.Config{
						SDKConfig:           config.SDKConfig{ForceModelPrefix: true},
						OAuthExcludedModels: map[string][]string{"openai-compatibility": {pluginStaticGlobalModel}},
					}
					if tc.configured {
						entry := config.OpenAICompatibility{Name: name}
						if tc.native {
							entry.Models = []config.OpenAICompatibilityModel{{Name: "native-kept"}}
						}
						cfg.OpenAICompatibility = []config.OpenAICompatibility{entry}
					}
					service := &Service{cfg: cfg, pluginHost: newPluginStaticModelHost(t, providerKey)}
					auth := &coreauth.Auth{
						ID:         t.Name(),
						Provider:   "openai-compatibility",
						Prefix:     "team",
						Status:     coreauth.StatusActive,
						Attributes: map[string]string{"auth_kind": "oauth", "compat_name": name, "provider_key": providerKey},
					}
					if ownRules {
						auth.Attributes["excluded_models"] = pluginStaticCredentialModel
					}
					reg := GlobalModelRegistry()
					t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
					var cache *openAICompatibilityRegistrationCache
					if tc.cached {
						cache = newOpenAICompatibilityRegistrationCacheForConfig(cfg)
					}
					if !service.registerModelsForAuthWithCache(t.Context(), auth, cache) {
						t.Fatal("current compatible registration was rejected")
					}
					for id, want := range map[string]bool{
						pluginStaticGlobalModel:     ownRules,
						pluginStaticCredentialModel: !ownRules,
						"native-kept":               tc.native,
					} {
						if got := reg.ClientSupportsModel(auth.ID, "team/"+id); got != want {
							t.Errorf("model %s availability = %v, want %v", id, got, want)
						}
						if reg.ClientSupportsModel(auth.ID, id) {
							t.Errorf("model %s bypassed the required prefix", id)
						}
					}
					if got := len(service.pluginHost.ModelsForProvider(providerKey)); got != 2 {
						t.Fatalf("source plugin catalog changed: %d models", got)
					}
				})
			}
		})
	}
}
