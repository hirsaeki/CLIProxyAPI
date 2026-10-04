package cliproxy

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// resolvePluginModelsForAuth is a narrow fork integration seam that keeps
// native model candidate handling out of upstream-owned service files.
var resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, auth *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
	if host == nil {
		return pluginhost.AuthModelResult{}
	}
	return host.ModelsForAuth(ctx, auth, candidates)
}

// Cached native catalogs must still pass through plugin discovery before the
// upstream fenced-publication shortcut. Candidates retain raw model IDs so
// aliases and prefixes are applied only after the plugin resolves its models.
func (s *Service) tryRegisterPluginModelsForAntigravityHints(ctx context.Context, auth *coreauth.Auth, authKind string, excluded []string, hints antigravityModelCapabilityHints) bool {
	if s == nil || s.pluginHost == nil {
		return false
	}
	models := filterAntigravityModels(registry.GetAntigravityModels(), hints)
	models = applyAntigravityFetchedModelCapabilities(models, hints)
	models = s.applyOAuthModelAvailability("antigravity", auth.ID, authKind, models)
	models = applyExcludedModels(models, excluded)
	return s.tryRegisterPluginModelsForAuth(ctx, auth, "antigravity", authKind, excluded, models)
}

func providerHasNativeModelCandidates(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case constant.Gemini,
		constant.GeminiInteractions,
		"vertex",
		"aistudio",
		"antigravity",
		"claude",
		"codex",
		"kimi",
		"xai":
		return true
	default:
		return false
	}
}
