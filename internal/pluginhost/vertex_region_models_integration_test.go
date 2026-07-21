//go:build cgo && (linux || darwin || freebsd)

package pluginhost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"gopkg.in/yaml.v3"
)

func TestVertexRegionModelsPluginCABI(t *testing.T) {
	pluginPath := os.Getenv("CLIPROXY_VERTEX_REGION_MODELS_PLUGIN")
	if pluginPath == "" {
		t.Skip("CLIPROXY_VERTEX_REGION_MODELS_PLUGIN is not set")
	}
	pluginPath, errAbs := filepath.Abs(pluginPath)
	if errAbs != nil {
		t.Fatalf("filepath.Abs() error = %v", errAbs)
	}
	if _, errStat := os.Stat(pluginPath); errStat != nil {
		t.Fatalf("stat plugin: %v", errStat)
	}

	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><table>
<thead><tr><th></th><th>Iowa (us-central1)</th></tr></thead>
<tbody>
<tr><td>Allowed <code>(gemini-allowed)</code></td><td aria-label="Supported"></td></tr>
<tr><td><a>Gemini 3.1 Pro</a> <code>(gemini-3.1-pro-preview)</code></td><td aria-label="Supported"></td></tr>
<tr><td><a>Gemini Docs Only</a> <code>(gemini-docs-only)</code></td><td aria-label="Supported"></td></tr>
<tr><td>Blocked <code>(gemini-blocked)</code></td><td></td></tr>
</tbody></table>`))
	}))
	t.Cleanup(docs.Close)

	var raw yaml.Node
	if errUnmarshal := yaml.Unmarshal([]byte(fmt.Sprintf("docs_url: %q\n", docs.URL)), &raw); errUnmarshal != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", errUnmarshal)
	}
	enabled := true
	pluginID := pluginIDFromPath(pluginPath)
	host := New()
	t.Cleanup(host.ShutdownAll)
	host.ApplyConfig(context.Background(), &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: true,
			Dir:     filepath.Dir(pluginPath),
			Configs: map[string]config.PluginInstanceConfig{
				pluginID: {Enabled: &enabled, Raw: *raw.Content[0]},
			},
		},
	})

	records := host.activeRecords()
	if len(records) != 1 {
		t.Fatalf("active plugin records = %d, want 1", len(records))
	}
	caps := records[0].plugin.Capabilities
	if caps.ModelProvider == nil {
		t.Fatal("ModelProvider = nil")
	}
	if caps.ModelRegistrar != nil || caps.AuthProvider != nil || caps.Executor != nil {
		t.Fatal("model-only plugin claimed registrar, auth, or executor capabilities")
	}
	if !reflect.DeepEqual(caps.ModelProviderIdentifiers, []string{"vertex"}) {
		t.Fatalf("ModelProviderIdentifiers = %#v", caps.ModelProviderIdentifiers)
	}

	thinking := &registry.ThinkingSupport{Min: 0, Max: 24576, ZeroAllowed: true, DynamicAllowed: true}
	result := host.ModelsForAuth(context.Background(), &coreauth.Auth{
		ID:       "vertex-auth",
		Provider: "vertex",
		Metadata: map[string]any{"location": "us-central1"},
	}, []*registry.ModelInfo{
		{ID: "gemini-allowed", DisplayName: "Allowed", InputTokenLimit: 100, Thinking: thinking},
		{ID: "gemini-3.1-pro", DisplayName: "Gemini 3.1 Pro", InputTokenLimit: 200, Thinking: thinking},
		{ID: "gemini-blocked", DisplayName: "Blocked"},
	})
	if result.Err != nil {
		t.Fatalf("ModelsForAuth() error = %v", result.Err)
	}
	if !result.Handled || result.Provider != "vertex" || len(result.Models) != 3 {
		t.Fatalf("ModelsForAuth() = %#v, want three handled Vertex models", result)
	}
	if result.Models[0].ID != "gemini-allowed" || result.Models[0].DisplayName != "Allowed" || result.Models[0].InputTokenLimit != 100 {
		t.Fatalf("filtered model metadata = %#v", result.Models[0])
	}
	if !reflect.DeepEqual(result.Models[0].Thinking, thinking) {
		t.Fatalf("filtered model thinking = %#v, want %#v", result.Models[0].Thinking, thinking)
	}
	if result.Models[1].ID != "gemini-3.1-pro-preview" || result.Models[1].InputTokenLimit != 200 || !reflect.DeepEqual(result.Models[1].Thinking, thinking) {
		t.Fatalf("preview model metadata = %#v", result.Models[1])
	}
	if result.Models[2].ID != "gemini-docs-only" || result.Models[2].DisplayName != "Gemini Docs Only" || result.Models[2].OwnedBy != "google" {
		t.Fatalf("documentation-only model metadata = %#v", result.Models[2])
	}
}
