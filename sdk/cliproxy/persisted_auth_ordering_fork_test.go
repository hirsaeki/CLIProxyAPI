package cliproxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/watcher"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/tidwall/gjson"
)

// A file snapshot can be queued before persistence but consumed before the
// persistence hook. Draining both operations must still retain the persisted state.
func TestPersistedAuthSurvivesPreHookWatcherSnapshotFork(t *testing.T) {
	ctx := coreauth.WithSkipPersist(context.Background())
	dir := t.TempDir()
	authID := "pre-hook-ordering.json"
	path := filepath.Join(dir, authID)
	writePrefix := func(prefix string) {
		t.Helper()
		content := fmt.Sprintf(`{"type":"codex","prefix":%q,"access_token":"test-token"}`, prefix)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePrefix("pfx-8")
	cfg := &config.Config{AuthDir: dir}
	w, err := watcher.NewWatcher("", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.SetConfig(cfg)
	t.Cleanup(func() {
		if errStop := w.Stop(); errStop != nil {
			t.Error(errStop)
		}
	})
	snapshots, err := w.SnapshotAuthFile(path)
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("snapshot count=%d, error=%v", len(snapshots), err)
	}
	initial := snapshots[0]
	if initial.Generation != 0 || initial.Prefix != "pfx-8" {
		t.Fatalf("unexpected watcher snapshot prefix=%q generation=%d", initial.Prefix, initial.Generation)
	}
	reg := GlobalModelRegistry()
	reg.UnregisterClient(authID)
	t.Cleanup(func() { reg.UnregisterClient(authID) })
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(&syncTestExecutor{})
	registered, err := manager.Register(ctx, initial.Clone())
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		cfg:         cfg,
		coreManager: manager,
		watcher: &WatcherWrapper{
			dispatchPersistedAuthWithRev: w.DispatchPersistedAuthUpdateWithRevision,
			snapshotAuthFile:            w.SnapshotAuthFile,
		},
	}
	hook := service.runtimeAuthSyncHook()
	if err := hook(ctx, registered); err != nil {
		t.Fatal(err)
	}
	baseModel := ""
	for _, model := range reg.GetModelsForClient(authID) {
		if strings.HasPrefix(model.ID, "pfx-8/") {
			baseModel = strings.TrimPrefix(model.ID, "pfx-8/")
			break
		}
	}
	if baseModel == "" {
		t.Fatal("initial prefixed model registration is missing")
	}

	stale := watcher.AuthUpdate{Action: watcher.AuthUpdateActionModify, ID: authID, Auth: initial.Clone()}
	_, revision := w.DispatchPersistedAuthUpdateWithRevision(&stale)
	if revision != 2 {
		t.Fatalf("queued snapshot revision=%d, want 2", revision)
	}

	// Model the completed persistence step while retaining the returned snapshot
	// that the management handler will pass to its post-persist hook.
	writePrefix("pfx-9")
	fresh, ok := manager.GetByID(authID)
	if !ok {
		t.Fatal("registered auth disappeared")
	}
	fresh.Prefix = "pfx-9"
	fresh.Metadata["prefix"] = "pfx-9"
	persisted, err := manager.Update(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}

	service.handleAuthUpdate(ctx, stale)
	if err := hook(ctx, persisted); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(data, "prefix").String(); got != "pfx-9" {
		t.Errorf("persisted prefix=%q, want pfx-9", got)
	}
	current, ok := manager.GetByID(authID)
	if !ok {
		t.Fatal("final auth is missing")
	}
	if current.Prefix != "pfx-9" {
		t.Errorf("runtime prefix=%q generation=%d, want pfx-9 after all work completed", current.Prefix, current.Generation)
	}
	if !reg.ClientSupportsModel(authID, "pfx-9/"+baseModel) {
		t.Errorf("registry did not retain persisted prefix pfx-9 for model %q", baseModel)
	}
}
