package registry

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestForkCatalogSourceRefreshPreservesLocalOverlay(t *testing.T) {
	for _, sourceKind := range []string{"default", "http", "file", "embedded"} {
		t.Run(sourceKind, func(t *testing.T) {
			restoreModelsCatalogForTest(t)
			restoreModelRefreshCallbackForTest(t)
			t.Setenv(localModelsOverlayEnv, "")
			base := testModelsCatalog()
			data := mustMarshalCatalog(t, base)
			if errLoad := loadModelsFromBytes(data, "test"); errLoad != nil {
				t.Fatal(errLoad)
			}
			overlayPath := writeModelsOverlayFile(t, staticModelsJSON{
				Gemini: []*ModelInfo{{ID: "gemini-local-first", DisplayName: "First overlay"}},
				Vertex: []*ModelInfo{{ID: base.Vertex[0].ID, DisplayName: "Vertex override"}},
			})
			t.Setenv(localModelsOverlayEnv, overlayPath)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(data)
			}))
			t.Cleanup(server.Close)
			source := ""
			switch sourceKind {
			case "http":
				source = server.URL
			case "file":
				source = filepath.Join(t.TempDir(), "catalog.json")
				if errWrite := os.WriteFile(source, data, 0o600); errWrite != nil {
					t.Fatal(errWrite)
				}
			case "embedded":
				source = embeddedCatalogSource
			}
			updater := &catalogUpdater{
				fetch:   catalogFetcher(data, []string{server.URL}, validateCatalogBytes),
				publish: publishCatalogBytes,
			}
			var notified []string
			SetModelRefreshCallback(func(changed []string) { notified = append(notified, changed...) })
			updater.refresh(t.Context(), source, 0)
			if lookupModelByID(GetGeminiModels(), "gemini-local-first") == nil || lookupModelByID(GetGeminiModels(), base.Gemini[0].ID) == nil {
				t.Fatal("catalog refresh lost the local addition or base model")
			}
			if model := lookupModelByID(GetGeminiVertexModels(), base.Vertex[0].ID); model == nil || model.DisplayName != "Vertex override" {
				t.Fatal("catalog refresh lost the overlay replacement")
			}
			if !slices.Contains(notified, "gemini") || !slices.Contains(notified, "vertex") {
				t.Fatalf("overlay changes were missing from refresh notification: %v", notified)
			}
			notified = nil
			updater.refresh(t.Context(), source, 0)
			if len(notified) != 0 {
				t.Fatalf("unchanged overlay caused a refresh notification: %v", notified)
			}
			updated := staticModelsJSON{Gemini: []*ModelInfo{{ID: "gemini-local-second"}}}
			if errWrite := os.WriteFile(overlayPath, mustMarshalCatalog(t, updated), 0o600); errWrite != nil {
				t.Fatal(errWrite)
			}
			updater.refresh(t.Context(), source, 0)
			if lookupModelByID(GetGeminiModels(), "gemini-local-first") != nil || lookupModelByID(GetGeminiModels(), "gemini-local-second") == nil {
				t.Fatal("catalog refresh did not replace the changed overlay")
			}
		})
	}
}

func TestForkCatalogPublicationIgnoresUnavailableOverlay(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "invalid"}[invalid], func(t *testing.T) {
			restoreModelsCatalogForTest(t)
			path := filepath.Join(t.TempDir(), "overlay.json")
			if invalid {
				if errWrite := os.WriteFile(path, []byte(`{"gemini":[{"id":""}]}`), 0o600); errWrite != nil {
					t.Fatal(errWrite)
				}
			}
			t.Setenv(localModelsOverlayEnv, path)
			base := testModelsCatalog()
			if _, errPublish := publishCatalogBytes(mustMarshalCatalog(t, base)); errPublish != nil {
				t.Fatal(errPublish)
			}
			if lookupModelByID(GetGeminiModels(), base.Gemini[0].ID) == nil {
				t.Fatal("unavailable overlay blocked valid base catalog publication")
			}
		})
	}
}
