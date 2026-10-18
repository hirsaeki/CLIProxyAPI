// Package forkcatalog supplies scoped historical fixtures for catalog behavior tests.
// It is imported only by tests; live catalog refresh and validation stay unchanged.
package forkcatalog

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

//go:embed legacy_codex_fork.json
var legacyCodexJSON []byte

// UseLegacyCodex installs historical template metadata for a non-parallel test.
// Other current models remain untouched. Defer the returned restore function;
// it must run before testing cleanup, which cannot start a synctest bubble.
// Do not use this helper alongside an active service catalog updater.
func UseLegacyCodex(t *testing.T) func() {
	t.Helper()
	original := registry.GetCodexClientModelsJSON()
	var current, fixture struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(original, &current); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(legacyCodexJSON, &fixture); err != nil {
		t.Fatal(err)
	}
	replaced := make(map[string]bool)
	for _, model := range fixture.Models {
		replaced[model["slug"].(string)] = true
	}
	for _, model := range current.Models {
		if !replaced[model["slug"].(string)] {
			fixture.Models = append(fixture.Models, model)
		}
	}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "fixture.json")
	originalPath := filepath.Join(dir, "original.json")
	for path, contents := range map[string][]byte{fixturePath: data, originalPath: original} {
		if err := registry.ValidateCodexClientModelsJSON(contents); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	publish(t, fixturePath, data)
	return func() { publish(t, originalPath, original) }
}

func publish(t *testing.T, path string, want []byte) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Home mode disables the unrelated general and Devin catalog readers.
		registry.StartModelCatalogUpdaters(ctx, registry.CatalogSources{CodexCatalog: path}, true)
		synctest.Wait()
		if !bytes.Equal(registry.GetCodexClientModelsJSON(), want) {
			t.Fatal("catalog fixture publication did not preserve the expected bytes")
		}
		cancel()
		synctest.Wait()
	})
}
