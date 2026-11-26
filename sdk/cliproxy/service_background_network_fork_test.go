package cliproxy

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// These lifecycle fixtures are not testing provider discovery. Keep background
// version and location requests local, even when the test runner has internet.
func startRejectingBackgroundProxyFork(t *testing.T) string {
	t.Helper()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider traffic is disabled in this test", http.StatusForbidden)
	}))
	t.Cleanup(proxy.Close)
	return proxy.URL
}

func localServiceTestCatalogsFork(t *testing.T) config.ModelCatalogs {
	t.Helper()
	root, errAbs := filepath.Abs(filepath.Join("..", "..", "internal", "registry", "models"))
	if errAbs != nil {
		t.Fatal(errAbs)
	}
	return config.ModelCatalogs{
		Catalog:      filepath.Join(root, "models.json"),
		CodexCatalog: filepath.Join(root, "codex_client_models.json"),
		DevinCatalog: filepath.Join(root, "devin_models.json"),
	}
}
