package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestReloadConfigPreservesWriteDuringCallbackFork(t *testing.T) {
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if errMkdir := os.MkdirAll(authDir, 0o755); errMkdir != nil {
		t.Fatalf("create auth directory: %v", errMkdir)
	}
	configPath := filepath.Join(tmpDir, "config.yaml")
	writeConfig := func(port int) {
		t.Helper()
		data := fmt.Sprintf("port: %d\nauth-dir: %q\n", port, filepath.ToSlash(authDir))
		if errWrite := os.WriteFile(configPath, []byte(data), 0o600); errWrite != nil {
			t.Fatalf("write config: %v", errWrite)
		}
	}

	const firstPort, secondPort = 8317, 8318
	writeConfig(firstPort)
	var loadedPorts []int
	w := &Watcher{
		configPath: configPath,
		authDir:    authDir,
		reloadCallback: func(cfg *config.Config) {
			loadedPorts = append(loadedPorts, cfg.Port)
			if len(loadedPorts) == 1 {
				// The first configuration is already loaded. A newer write during
				// runtime updates must remain eligible for the next reload.
				writeConfig(secondPort)
			}
		},
	}

	w.reloadConfigIfChanged()
	if len(loadedPorts) != 1 || loadedPorts[0] != firstPort {
		t.Fatalf("first reload ports = %v, want [%d]", loadedPorts, firstPort)
	}
	w.reloadConfigIfChanged()
	if len(loadedPorts) != 2 || loadedPorts[1] != secondPort {
		t.Fatalf("reload ports = %v, want [%d %d]; newer write was incorrectly marked as loaded", loadedPorts, firstPort, secondPort)
	}
	w.clientsMutex.RLock()
	current := w.config
	w.clientsMutex.RUnlock()
	if current == nil || current.Port != secondPort {
		t.Fatalf("current config = %#v, want port %d", current, secondPort)
	}

	w.reloadConfigIfChanged()
	if len(loadedPorts) != 2 {
		t.Fatalf("unchanged config triggered another reload: %v", loadedPorts)
	}
}

func TestReloadConfigIgnoresEmptyLoadAfterHashCheckFork(t *testing.T) {
	configPath, authDir := newIssue6631ConfigFile(t)
	initial := []byte(fmt.Sprintf("port: 8317\nauth-dir: %q\n", filepath.ToSlash(authDir)))
	candidate := []byte(fmt.Sprintf("port: 8318\nauth-dir: %q\n", filepath.ToSlash(authDir)))
	if errWrite := os.WriteFile(configPath, initial, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}

	var loadedPorts []int
	w := &Watcher{
		configPath: configPath,
		authDir:    authDir,
		reloadCallback: func(cfg *config.Config) {
			loadedPorts = append(loadedPorts, cfg.Port)
		},
	}
	w.ReloadConfigIfChanged()
	if len(loadedPorts) != 1 || loadedPorts[0] != 8317 {
		t.Fatalf("initial reload ports = %v, want [8317]", loadedPorts)
	}
	appliedConfig := w.config
	appliedHash := w.lastConfigHash
	appliedYAML := string(w.oldConfigYaml)
	if errWrite := os.WriteFile(configPath, candidate, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}

	originalLoad := loadConfigFileFunc
	t.Cleanup(func() { loadConfigFileFunc = originalLoad })
	var parsedHash string
	loadConfigFileFunc = func(path string) (*config.Config, error) {
		// Deterministically expose the save's truncate window only to the loader,
		// after the first nonempty hash read. Restore the file before it returns.
		if errWrite := os.WriteFile(path, nil, 0o600); errWrite != nil {
			return nil, errWrite
		}
		cfg, errLoad := originalLoad(path)
		if cfg != nil {
			parsedHash = cfg.SourceHash
		}
		if errWrite := os.WriteFile(path, candidate, 0o600); errWrite != nil {
			return nil, errWrite
		}
		return cfg, errLoad
	}
	w.ReloadConfigIfChanged()
	if parsedHash != config.ComputeSourceHash(nil) {
		t.Fatalf("injected loader source hash = %q, want empty snapshot hash", parsedHash)
	}
	if len(loadedPorts) != 1 {
		t.Fatalf("empty snapshot triggered a callback: %v", loadedPorts)
	}
	if w.config != appliedConfig || w.authDir != authDir || string(w.oldConfigYaml) != appliedYAML {
		t.Fatal("empty snapshot changed the applied config or auth directory")
	}
	if w.lastConfigHash != appliedHash {
		t.Fatalf("empty snapshot advanced applied hash: got %q, want %q", w.lastConfigHash, appliedHash)
	}

	loadConfigFileFunc = originalLoad
	w.ReloadConfigIfChanged()
	if len(loadedPorts) != 2 || loadedPorts[1] != 8318 {
		t.Fatalf("restored candidate reload ports = %v, want [8317 8318]", loadedPorts)
	}
	if w.lastConfigHash != config.ComputeSourceHash(candidate) {
		t.Fatalf("candidate hash = %q, want restored snapshot hash", w.lastConfigHash)
	}
}
