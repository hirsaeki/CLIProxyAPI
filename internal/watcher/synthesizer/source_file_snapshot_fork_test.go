package synthesizer

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestFileSynthesisPropagatesSourceFileSnapshotFork(t *testing.T) {
	for _, mode := range []string{"full-scan", "single-file"} {
		for _, provider := range []string{"builtin", "plugin-multi"} {
			t.Run(mode+"/"+provider, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "auth.json")
				raw := []byte(`{"type":"claude","access_token":"old"}`)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				ctx := &SynthesisContext{AuthDir: dir, Config: &config.Config{}, Now: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)}
				want := 1
				if provider == "plugin-multi" {
					want = 2
					ctx.PluginAuthParser = multiAuthParserFunc(func(context.Context, pluginapi.AuthParseRequest) ([]*coreauth.Auth, bool, error) {
						return []*coreauth.Auth{
							{ID: "plugin-one", Provider: "plugin", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "plugin"}},
							nil,
							{ID: "plugin-two", Provider: "plugin", Status: coreauth.StatusActive, Metadata: map[string]any{"type": "plugin"}},
						}, true, nil
					})
				}
				var auths []*coreauth.Auth
				var err error
				if mode == "full-scan" {
					auths, err = NewFileSynthesizer().Synthesize(ctx)
				} else {
					auths, err = SynthesizeAuthFile(ctx, path, raw)
				}
				if err != nil || len(auths) != want {
					t.Fatalf("synthesis returned %d auths, want %d: %v", len(auths), want, err)
				}
				manager := coreauth.NewManager(nil, nil, nil)
				saveCtx := coreauth.WithSkipPersist(context.Background())
				for _, auth := range auths {
					if auth.Generation != 0 {
						t.Fatalf("synthesized auth %q is already versioned", auth.ID)
					}
					if got, errRegister := manager.Register(saveCtx, auth.Clone()); errRegister != nil || got == nil {
						t.Fatalf("matching source for %q was rejected: %v", auth.ID, errRegister)
					}
				}
				if errWrite := os.WriteFile(path, []byte(`{"type":"claude","access_token":"new"}`), 0600); errWrite != nil {
					t.Fatal(errWrite)
				}
				for _, auth := range auths {
					before, _ := manager.GetByID(auth.ID)
					stale := auth.Clone()
					stale.Label = "stale-source"
					got, errUpdate := manager.Update(saveCtx, stale)
					if errUpdate != nil || !reflect.DeepEqual(got, before) {
						t.Fatalf("auth %q did not retain its source marker through synthesis and cloning: %v", auth.ID, errUpdate)
					}
					if current, _ := manager.GetByID(auth.ID); !reflect.DeepEqual(current, before) {
						t.Fatalf("stale synthesized auth %q changed the manager", auth.ID)
					}
					freshManager := coreauth.NewManager(nil, nil, nil)
					if registered, errRegister := freshManager.Register(saveCtx, auth.Clone()); errRegister != nil || registered != nil {
						t.Fatalf("stale synthesized auth %q was registered: %v", auth.ID, errRegister)
					}
				}
			})
		}
	}
}
