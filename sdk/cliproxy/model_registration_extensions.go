package cliproxy

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// resolvePluginModelsForAuth is a narrow fork integration seam that keeps
// native model candidate handling out of upstream-owned service files.
var resolvePluginModelsForAuth = func(host *pluginhost.Host, ctx context.Context, auth *coreauth.Auth, candidates []*ModelInfo) pluginhost.AuthModelResult {
	if host == nil {
		return pluginhost.AuthModelResult{}
	}
	return host.ModelsForAuth(ctx, auth, candidates)
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
