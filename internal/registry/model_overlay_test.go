package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModelsFromBytesAppliesLocalGoogleOverlay(t *testing.T) {
	restoreModelsCatalogForTest(t)

	base := testModelsCatalog()
	base.Gemini = []*ModelInfo{{ID: "gemini-base", DisplayName: "Gemini Base"}}
	base.Vertex = []*ModelInfo{{ID: "gemini-vertex", DisplayName: "Vertex Original"}}
	base.AIStudio = []*ModelInfo{{ID: "gemini-aistudio", DisplayName: "AI Studio Base"}}

	overlay := staticModelsJSON{
		Gemini: []*ModelInfo{{
			ID:          "gemini-local-preview",
			Object:      "model",
			OwnedBy:     "google",
			Type:        "gemini",
			DisplayName: "Gemini Local Preview",
			Thinking:    &ThinkingSupport{Min: 128, Max: 32768, DynamicAllowed: true},
		}},
		Vertex: []*ModelInfo{{
			ID:          "gemini-vertex",
			Object:      "model",
			OwnedBy:     "google",
			Type:        "gemini",
			DisplayName: "Vertex Overlay",
		}},
		AIStudio: []*ModelInfo{{
			ID:          "gemini-aistudio-local",
			Object:      "model",
			OwnedBy:     "google",
			Type:        "gemini",
			DisplayName: "AI Studio Local",
		}},
		XAI: []*ModelInfo{{ID: "ignored-local-xai", DisplayName: "Ignored XAI"}},
	}
	t.Setenv(localModelsOverlayEnv, writeModelsOverlayFile(t, overlay))

	if err := loadModelsFromBytes(mustMarshalCatalog(t, base), "test"); err != nil {
		t.Fatalf("loadModelsFromBytes() error = %v", err)
	}

	if got := lookupModelByID(GetGeminiModels(), "gemini-local-preview"); got == nil {
		t.Fatal("local Gemini overlay model was not registered")
	} else if got.Thinking == nil || got.Thinking.Max != 32768 || !got.Thinking.DynamicAllowed {
		t.Fatalf("local Gemini thinking metadata = %+v, want max 32768 dynamic", got.Thinking)
	}

	if got := lookupModelByID(GetGeminiVertexModels(), "gemini-vertex"); got == nil {
		t.Fatal("local Vertex overlay model was not registered")
	} else if got.DisplayName != "Vertex Overlay" {
		t.Fatalf("Vertex display name = %q, want overlay replacement", got.DisplayName)
	}

	if got := lookupModelByID(GetAIStudioModels(), "gemini-aistudio-local"); got == nil {
		t.Fatal("local AI Studio overlay model was not registered")
	}
	if got := lookupModelByID(GetXAIModels(), "ignored-local-xai"); got != nil {
		t.Fatalf("local overlay should not affect xai models, got %+v", got)
	}
}

func TestTryRefreshModelsKeepsLocalOverlayAfterRemoteRefresh(t *testing.T) {
	restoreModelsCatalogForTest(t)
	restoreModelRefreshCallbackForTest(t)
	originalURLs := append([]string(nil), modelsURLs...)
	t.Cleanup(func() {
		modelsURLs = originalURLs
	})

	base := testModelsCatalog()
	base.Gemini = []*ModelInfo{{ID: "gemini-remote", DisplayName: "Remote Gemini"}}
	overlay := staticModelsJSON{
		Gemini: []*ModelInfo{{
			ID:          "gemini-local-refresh",
			Object:      "model",
			OwnedBy:     "google",
			Type:        "gemini",
			DisplayName: "Gemini Local Refresh",
		}},
	}
	t.Setenv(localModelsOverlayEnv, writeModelsOverlayFile(t, overlay))

	if err := loadModelsFromBytes(mustMarshalCatalog(t, base), "initial"); err != nil {
		t.Fatalf("loadModelsFromBytes() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustMarshalCatalog(t, base))
	}))
	t.Cleanup(server.Close)
	modelsURLs = []string{server.URL}

	tryRefreshModels(t.Context(), "test refresh")

	if got := lookupModelByID(GetGeminiModels(), "gemini-local-refresh"); got == nil {
		t.Fatal("local Gemini overlay model disappeared after remote refresh")
	}
}

func TestTryRefreshModelsDoesNotNotifyWhenRemoteMatchesCurrentOverlay(t *testing.T) {
	restoreModelsCatalogForTest(t)
	restoreModelRefreshCallbackForTest(t)
	originalURLs := append([]string(nil), modelsURLs...)
	t.Cleanup(func() {
		modelsURLs = originalURLs
	})

	base := testModelsCatalog()
	base.Gemini = []*ModelInfo{{ID: "gemini-remote", DisplayName: "Remote Gemini"}}
	overlay := staticModelsJSON{
		Gemini: []*ModelInfo{{
			ID:          "gemini-local-stable",
			Object:      "model",
			OwnedBy:     "google",
			Type:        "gemini",
			DisplayName: "Gemini Local Stable",
		}},
	}
	t.Setenv(localModelsOverlayEnv, writeModelsOverlayFile(t, overlay))

	if err := loadModelsFromBytes(mustMarshalCatalog(t, base), "initial"); err != nil {
		t.Fatalf("loadModelsFromBytes() error = %v", err)
	}

	var notified []string
	SetModelRefreshCallback(func(changedProviders []string) {
		notified = append(notified, changedProviders...)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(mustMarshalCatalog(t, base))
	}))
	t.Cleanup(server.Close)
	modelsURLs = []string{server.URL}

	tryRefreshModels(t.Context(), "test refresh")

	if len(notified) != 0 {
		t.Fatalf("refresh callback called with %v, want no changes", notified)
	}
}

func TestInvalidLocalOverlayDoesNotBlockBaseCatalog(t *testing.T) {
	restoreModelsCatalogForTest(t)

	base := testModelsCatalog()
	base.Gemini = []*ModelInfo{{ID: "gemini-base", DisplayName: "Gemini Base"}}

	path := filepath.Join(t.TempDir(), "invalid-models.json")
	if err := os.WriteFile(path, []byte(`{"gemini":[{"id":""}]}`), 0o600); err != nil {
		t.Fatalf("write invalid overlay: %v", err)
	}
	t.Setenv(localModelsOverlayEnv, path)

	if err := loadModelsFromBytes(mustMarshalCatalog(t, base), "test"); err != nil {
		t.Fatalf("loadModelsFromBytes() error = %v", err)
	}

	if got := lookupModelByID(GetGeminiModels(), "gemini-base"); got == nil {
		t.Fatal("base catalog model missing after invalid overlay")
	}
}

func TestMissingLocalOverlayDoesNotBlockBaseCatalog(t *testing.T) {
	restoreModelsCatalogForTest(t)

	base := testModelsCatalog()
	base.Gemini = []*ModelInfo{{ID: "gemini-base", DisplayName: "Gemini Base"}}
	t.Setenv(localModelsOverlayEnv, filepath.Join(t.TempDir(), "missing-models.json"))

	if err := loadModelsFromBytes(mustMarshalCatalog(t, base), "test"); err != nil {
		t.Fatalf("loadModelsFromBytes() error = %v", err)
	}

	if got := lookupModelByID(GetGeminiModels(), "gemini-base"); got == nil {
		t.Fatal("base catalog model missing after missing overlay")
	}
}

func restoreModelsCatalogForTest(t *testing.T) {
	t.Helper()

	modelsCatalogStore.mu.RLock()
	previous := cloneStaticModelsCatalog(modelsCatalogStore.data)
	modelsCatalogStore.mu.RUnlock()

	t.Cleanup(func() {
		modelsCatalogStore.mu.Lock()
		modelsCatalogStore.data = previous
		modelsCatalogStore.mu.Unlock()
	})
}

func restoreModelRefreshCallbackForTest(t *testing.T) {
	t.Helper()

	refreshCallbackMu.Lock()
	previousCallback := refreshCallback
	previousPending := append([]string(nil), pendingRefreshChanges...)
	refreshCallback = nil
	pendingRefreshChanges = nil
	refreshCallbackMu.Unlock()

	t.Cleanup(func() {
		refreshCallbackMu.Lock()
		refreshCallback = previousCallback
		pendingRefreshChanges = previousPending
		refreshCallbackMu.Unlock()
	})
}

func testModelsCatalog() staticModelsJSON {
	return staticModelsJSON{
		Claude:      []*ModelInfo{{ID: "claude-test"}},
		Gemini:      []*ModelInfo{{ID: "gemini-test"}},
		Vertex:      []*ModelInfo{{ID: "vertex-test"}},
		AIStudio:    []*ModelInfo{{ID: "aistudio-test"}},
		CodexFree:   []*ModelInfo{{ID: "codex-free-test"}},
		CodexTeam:   []*ModelInfo{{ID: "codex-team-test"}},
		CodexPlus:   []*ModelInfo{{ID: "codex-plus-test"}},
		CodexPro:    []*ModelInfo{{ID: "codex-pro-test"}},
		Kimi:        []*ModelInfo{{ID: "kimi-test"}},
		Antigravity: []*ModelInfo{{ID: "antigravity-test"}},
		XAI:         []*ModelInfo{{ID: "xai-test"}},
	}
}

func mustMarshalCatalog(t *testing.T, catalog staticModelsJSON) []byte {
	t.Helper()

	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	return data
}

func writeModelsOverlayFile(t *testing.T, overlay staticModelsJSON) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "local-models.json")
	if err := os.WriteFile(path, mustMarshalCatalog(t, overlay), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	return path
}

func lookupModelByID(models []*ModelInfo, id string) *ModelInfo {
	for _, model := range models {
		if model != nil && model.ID == id {
			return model
		}
	}
	return nil
}
