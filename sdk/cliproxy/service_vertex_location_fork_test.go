package cliproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	management "github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	fileauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestVertexLocationPatchPersistsAndReregistersModels(t *testing.T) {
	const name = "vertex-location-patch.json"
	ctx := context.Background()
	cfg := &config.Config{AuthDir: t.TempDir()}
	path := filepath.Join(cfg.AuthDir, name)
	initial := `{"type":"vertex","project_id":"test-project","location":"us-central1","service_account":{"project_id":"test-project"}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	load := func() *coreauth.Manager {
		t.Helper()
		store := fileauth.NewFileTokenStore()
		store.SetBaseDir(cfg.AuthDir)
		manager := coreauth.NewManager(store, nil, nil)
		if err := manager.Load(ctx); err != nil {
			t.Fatal(err)
		}
		return manager
	}
	manager := load()

	originalResolver := resolvePluginModelsForAuth
	t.Cleanup(func() { resolvePluginModelsForAuth = originalResolver })
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(name) })
	const modelPrefix = "gemini-test-location-"
	resolvePluginModelsForAuth = func(_ *pluginhost.Host, _ context.Context, auth *coreauth.Auth, _ []*ModelInfo) pluginhost.AuthModelResult {
		location, _ := auth.Metadata["location"].(string)
		return pluginhost.AuthModelResult{
			Provider: "vertex",
			Models:   []*ModelInfo{{ID: modelPrefix + location, Type: "vertex"}},
			Handled:  true,
		}
	}

	service := &Service{cfg: cfg, coreManager: manager, pluginHost: pluginhost.New()}
	hook := service.runtimeAuthSyncHook()
	handler := management.NewHandlerWithoutConfigFilePath(cfg, manager)
	handler.SetPostAuthPersistHook(hook)
	router := gin.New()
	router.PATCH("/v8/management/credentials/fields", handler.PatchAuthFileFields)

	initialAuth, ok := manager.GetByID(name)
	if !ok {
		t.Fatal("initial Vertex credential not loaded")
	}
	if err := hook(ctx, initialAuth); err != nil {
		t.Fatal(err)
	}
	checkModels := func(location string) {
		t.Helper()
		models := GlobalModelRegistry().GetModelsForClient(name)
		if len(models) != 1 || models[0].ID != modelPrefix+location {
			t.Fatalf("registered models = %v, want only %s", models, modelPrefix+location)
		}
	}
	checkModels("us-central1")

	for _, location := range []string{"global", "us", "eu", "europe-west4"} {
		t.Run(location, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/v8/management/credentials/fields",
				strings.NewReader(`{"name":"`+name+`","location":"`+location+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body.String())
			}
			checkModels(location)

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted struct {
				Location       string         `json:"location"`
				ProjectID      string         `json:"project_id"`
				ServiceAccount map[string]any `json:"service_account"`
			}
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.Location != location || persisted.ProjectID != "test-project" || persisted.ServiceAccount["project_id"] != "test-project" {
				t.Fatal("disk credential did not preserve the new location and existing service account")
			}

			for _, candidate := range []*coreauth.Manager{manager, load()} {
				auth, found := candidate.GetByID(name)
				if !found || auth.Metadata["location"] != location {
					t.Fatalf("runtime or reloaded credential does not have location %q", location)
				}
			}
		})
	}
}
