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
