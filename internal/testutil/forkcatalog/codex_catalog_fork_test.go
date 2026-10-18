package forkcatalog

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func TestLegacyCodexFixtureRestoresCatalogFork(t *testing.T) {
	original := registry.GetCodexClientModelsJSON()
	t.Run("historical metadata", func(t *testing.T) {
		defer UseLegacyCodex(t)()
		var payload struct {
			Models []map[string]any `json:"models"`
		}
		if err := json.Unmarshal(registry.GetCodexClientModelsJSON(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, model := range payload.Models {
			if model["slug"] == "gpt-5.5" {
				if model["apply_patch_tool_type"] != "freeform" || model["multi_agent_version"] != nil {
					t.Fatal("historical template capabilities changed")
				}
				return
			}
		}
		t.Fatal("historical template is missing")
	})
	if !bytes.Equal(registry.GetCodexClientModelsJSON(), original) {
		t.Fatal("fixture did not restore the exact original catalog")
	}
}
