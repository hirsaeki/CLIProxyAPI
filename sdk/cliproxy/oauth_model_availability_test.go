package cliproxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	internalregistry "github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestLoadOAuthModelAvailability(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "availability.json")
	raw := `{
  "schema_version": 1,
  "generated_at": "2026-07-25T00:00:00Z",
  "credentials": [{
    "provider": "claude",
    "auth_id": "claude-user",
    "client": {"name": "@anthropic-ai/claude-agent-sdk", "version": "0.3.219", "artifact_sha256": "abc"},
    "models": [{"id": "claude-known", "display_name": "Known"}]
  }]
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, resolved, err := loadOAuthModelAvailability("availability.json", filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("loadOAuthModelAvailability: %v", err)
	}
	if resolved != path {
		t.Fatalf("resolved path = %q, want %q", resolved, path)
	}
	entry, ok := snapshot.lookup("CLAUDE", "claude-user")
	if !ok || len(entry.Models) != 1 || entry.Models[0].ID != "claude-known" {
		t.Fatalf("lookup = %#v, %v", entry, ok)
	}
}

func TestLoadOAuthModelAvailabilityRejectsInvalidDocuments(t *testing.T) {
	tests := map[string]string{
		"unsupported schema":   `{"schema_version":2,"generated_at":"2026-07-25T00:00:00Z","credentials":[]}`,
		"unsupported provider": `{"schema_version":1,"generated_at":"2026-07-25T00:00:00Z","credentials":[{"provider":"openai","auth_id":"a","models":[{"id":"m"}]}]}`,
		"empty auth id":        `{"schema_version":1,"generated_at":"2026-07-25T00:00:00Z","credentials":[{"provider":"claude","auth_id":"","models":[{"id":"m"}]}]}`,
		"empty models":         `{"schema_version":1,"generated_at":"2026-07-25T00:00:00Z","credentials":[{"provider":"xai","auth_id":"a","models":[]}]}`,
		"duplicate credential": `{"schema_version":1,"generated_at":"2026-07-25T00:00:00Z","credentials":[{"provider":"xai","auth_id":"a","models":[{"id":"m"}]},{"provider":"XAI","auth_id":"a","models":[{"id":"n"}]}]}`,
		"duplicate model":      `{"schema_version":1,"generated_at":"2026-07-25T00:00:00Z","credentials":[{"provider":"claude","auth_id":"a","models":[{"id":"m"},{"id":"M"}]}]}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "availability.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadOAuthModelAvailability(path, filepath.Join(t.TempDir(), "config.yaml")); err == nil {
				t.Fatal("error = nil, want validation error")
			}
		})
	}
}

func TestBuilderRejectsMissingConfiguredOAuthModelAvailability(t *testing.T) {
	dir := t.TempDir()
	_, err := NewBuilder().
		WithConfig(&config.Config{OAuthModelAvailabilityFile: "missing.json"}).
		WithConfigPath(filepath.Join(dir, "config.yaml")).
		Build()
	if err == nil {
		t.Fatal("Build error = nil, want configured sidecar error")
	}
}

func TestRegisterModelsForAuthAppliesOAuthAvailabilityBeforeExclusionsAndAliases(t *testing.T) {
	native := internalregistry.GetClaudeModels()
	if len(native) == 0 {
		t.Fatal("Claude catalog is empty")
	}
	nativeID := native[0].ID
	snapshot := &oauthModelAvailabilitySnapshot{entries: map[oauthModelAvailabilityKey]oauthModelAvailabilityCredential{
		{provider: "claude", authID: "claude-sidecar"}: {
			Provider: "claude",
			AuthID:   "claude-sidecar",
			Models: []oauthModelAvailabilityModel{
				{ID: nativeID},
				{ID: "claude-sidecar-only", DisplayName: "Sidecar Only", Description: "Official client model"},
			},
		},
	}}
	service := &Service{
		cfg: &config.Config{
			OAuthModelAlias: map[string][]config.OAuthModelAlias{
				"claude": {{Name: nativeID, Alias: "claude-aliased"}},
			},
		},
		oauthModelAvailability: snapshot,
	}
	auth := &coreauth.Auth{
		ID:       "claude-sidecar",
		Provider: "claude",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"auth_kind":       "oauth",
			"excluded_models": "claude-sidecar-only",
		},
	}
	registry := internalregistry.GetGlobalRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() { registry.UnregisterClient(auth.ID) })

	service.registerModelsForAuth(context.Background(), auth)
	models := registry.GetModelsForClient(auth.ID)
	if findModel(models, nativeID) != nil {
		t.Fatalf("unaliased model %q was registered", nativeID)
	}
	aliased := findModel(models, "claude-aliased")
	if aliased == nil {
		t.Fatal("aliased native model was not registered")
	}
	if aliased.ContextLength != native[0].ContextLength {
		t.Fatalf("native metadata was not preserved: context length = %d, want %d", aliased.ContextLength, native[0].ContextLength)
	}
	if findModel(models, "claude-sidecar-only") != nil {
		t.Fatal("excluded sidecar-only model was registered")
	}
}

func TestRegisterModelsForAuthAvailabilityFallbackAndAPIKeyBehavior(t *testing.T) {
	snapshot := &oauthModelAvailabilitySnapshot{entries: map[oauthModelAvailabilityKey]oauthModelAvailabilityCredential{
		{provider: "xai", authID: "matched"}: {
			Provider: "xai", AuthID: "matched", Models: []oauthModelAvailabilityModel{{ID: "grok-sidecar-only"}},
		},
	}}
	tests := []struct {
		name     string
		authID   string
		authKind string
		wantOnly bool
	}{
		{name: "matching oauth is authoritative", authID: "matched", authKind: "oauth", wantOnly: true},
		{name: "missing oauth falls back", authID: "missing", authKind: "oauth"},
		{name: "api key is unaffected", authID: "matched", authKind: "apikey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{cfg: &config.Config{}, oauthModelAvailability: snapshot}
			auth := &coreauth.Auth{ID: tt.authID, Provider: "xai", Status: coreauth.StatusActive, Attributes: map[string]string{"auth_kind": tt.authKind}}
			registry := internalregistry.GetGlobalRegistry()
			registry.UnregisterClient(auth.ID)
			t.Cleanup(func() { registry.UnregisterClient(auth.ID) })
			service.registerModelsForAuth(context.Background(), auth)
			models := registry.GetModelsForClient(auth.ID)
			if tt.wantOnly {
				if len(models) != 1 || models[0].ID != "grok-sidecar-only" {
					t.Fatalf("models = %#v, want sidecar-only", models)
				}
			} else if findModel(models, "grok-sidecar-only") != nil || len(models) < 2 {
				t.Fatalf("models = %#v, want native catalog without sidecar-only model", models)
			}
		})
	}
}

func findModel(models []*internalregistry.ModelInfo, id string) *internalregistry.ModelInfo {
	for _, model := range models {
		if model != nil && model.ID == id {
			return model
		}
	}
	return nil
}
