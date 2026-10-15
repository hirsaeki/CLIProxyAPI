package management

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func newConfigSourceRewriteHandlerFork(t *testing.T) (*Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(path, []byte("request-retry: 3\nmax-retry-credentials: 0\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	cfg, errLoad := config.LoadConfig(path)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	return &Handler{cfg: cfg, configFilePath: path}, path
}

func saveConfigSourceRewriteFork(t *testing.T, h *Handler, update func(*config.Config)) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(ConfigV8ContextKey, true)
	h.mu.Lock()
	update(h.cfg)
	_, saved := h.saveConfigAndSnapshotLocked(ctx)
	h.mu.Unlock()
	if !saved {
		t.Fatalf("save management config: %s", recorder.Body.String())
	}
}

func TestConfigLoaderRewritePublishesBeforeNextManagementSaveFork(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"plaintext secret", "request-retry: 9\nremote-management:\n  secret-key: fork-loader-secret\n"},
		{"plaintext v8 secret", "request-retry: 9\nmanagement:\n  secret-key: fork-loader-secret\n"},
		{"legacy layout", "request-retry: 3\nrouting:\n  retry:\n    request-retry: 9\n"},
		{"both rewrites", "request-retry: 3\nrouting:\n  retry:\n    request-retry: 9\nremote-management:\n  secret-key: fork-loader-secret\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, path := newConfigSourceRewriteHandlerFork(t)
			if errWrite := os.WriteFile(path, []byte(tc.raw), 0o600); errWrite != nil {
				t.Fatal(errWrite)
			}
			loaded, errLoad := config.LoadConfig(path)
			if errLoad != nil {
				t.Fatal(errLoad)
			}
			if loaded.RequestRetry != 9 {
				t.Fatalf("loaded request-retry = %d, want 9", loaded.RequestRetry)
			}
			saved, errRead := os.ReadFile(path)
			if errRead != nil {
				t.Fatal(errRead)
			}
			if loaded.SourceHash != config.ComputeSourceHash(saved) {
				t.Error("loader-owned rewrite was not represented by source provenance")
			}
			h.SetConfig(loaded)

			// The maintained management save path can run before the writeback's
			// next reload. It must preserve the external edit already loaded.
			saveConfigSourceRewriteFork(t, h, func(cfg *config.Config) { cfg.MaxRetryCredentials = 7 })
			disk := readIssue6631Disk(t, path)
			if disk.RequestRetry != 9 || disk.MaxRetryCredentials != 7 {
				t.Fatalf("config after save = request-retry %d, max-retry-credentials %d; want 9, 7", disk.RequestRetry, disk.MaxRetryCredentials)
			}
		})
	}
}

func TestConfigLoaderRewriteStillRejectsLaterFileRevisionFork(t *testing.T) {
	h, path := newConfigSourceRewriteHandlerFork(t)
	if errWrite := os.WriteFile(path, []byte("request-retry: 9\nremote-management:\n  secret-key: fork-loader-secret\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	loaded, errLoad := config.LoadConfig(path)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	// A newer management save supersedes even a normalized source snapshot.
	saveConfigSourceRewriteFork(t, h, func(cfg *config.Config) { cfg.RequestRetry = 11 })
	h.SetConfig(loaded)
	saveConfigSourceRewriteFork(t, h, func(cfg *config.Config) { cfg.MaxRetryCredentials = 7 })
	disk := readIssue6631Disk(t, path)
	if disk.RequestRetry != 11 || disk.MaxRetryCredentials != 7 {
		t.Fatalf("config after stale publication = request-retry %d, max-retry-credentials %d; want 11, 7", disk.RequestRetry, disk.MaxRetryCredentials)
	}
}
