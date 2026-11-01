package management

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/pluginhost"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestPluginLoginRollbackEmptyFileFork(t *testing.T) {
	for _, runtimeState := range []string{"no-manager", "absent", "existing"} {
		for _, superseded := range []bool{false, true} {
			name := runtimeState + "/restore"
			if superseded {
				name = runtimeState + "/superseded"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "account.json")
				if errWrite := os.WriteFile(path, nil, 0o600); errWrite != nil {
					t.Fatal(errWrite)
				}
				var manager *coreauth.Manager
				if runtimeState != "no-manager" {
					manager = coreauth.NewManager(nil, nil, nil)
				}
				authForToken := func(token string) *coreauth.Auth {
					return &coreauth.Auth{
						ID: "account.json", FileName: "account.json", Provider: "codex",
						Metadata:   map[string]any{"type": "codex", "access_token": token},
						Attributes: map[string]string{coreauth.AttributePath: path},
					}
				}
				if runtimeState == "existing" {
					if _, errRegister := manager.Register(t.Context(), authForToken("old-token")); errRegister != nil {
						t.Fatal(errRegister)
					}
				}
				h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: dir}, manager)
				h.tokenStore = sdkAuth.NewFileTokenStore()
				host := pluginhost.New()
				h.SetPluginHost(host)
				newAuth := host.AuthDataToCoreAuth(pluginapi.AuthData{
					Provider: "codex", ID: "account.json", FileName: "account.json",
					StorageJSON: []byte(`{"type":"codex","access_token":"failed-batch-token"}`),
					Metadata:    map[string]any{"type": "codex", "access_token": "failed-batch-token"},
				}, path, "account.json")
				failure := errors.New("second record rejected")
				const winner = `{"type":"codex","access_token":"winner-token"}`
				h.postAuthHook = func(ctx context.Context, auth *coreauth.Auth) error {
					if auth.ID != "fail.json" {
						return nil
					}
					current, errRead := os.ReadFile(path)
					if errRead != nil || !strings.Contains(string(current), "failed-batch-token") {
						t.Fatalf("first record was not persisted: %q, %v", current, errRead)
					}
					if superseded {
						if errWrite := os.WriteFile(path, []byte(winner), 0o600); errWrite != nil {
							t.Fatal(errWrite)
						}
						if manager != nil {
							if _, errUpdate := manager.Update(coreauth.WithSkipPersist(ctx), authForToken("winner-token")); errUpdate != nil {
								t.Fatal(errUpdate)
							}
						}
					}
					return failure
				}
				errSave := h.savePluginLoginRecords(t.Context(), []*coreauth.Auth{newAuth, {
					ID: "fail.json", FileName: "fail.json", Provider: "codex",
					Metadata: map[string]any{"type": "codex"},
				}})
				if !errors.Is(errSave, failure) {
					t.Fatalf("batch error = %v, want second-record failure", errSave)
				}
				got, errRead := os.ReadFile(path)
				want := ""
				if superseded {
					want = winner
				}
				if errRead != nil || string(got) != want {
					t.Fatalf("restored file = %q, %v; want exact bytes %q", got, errRead, want)
				}
				if manager != nil {
					current, exists := manager.GetByID("account.json")
					wantExists := runtimeState == "existing" || superseded
					if exists != wantExists {
						t.Fatalf("runtime auth exists = %v, want %v", exists, wantExists)
					}
					if wantExists {
						wantToken := "old-token"
						if superseded {
							wantToken = "winner-token"
						}
						if current == nil || current.Metadata["access_token"] != wantToken {
							t.Fatalf("runtime auth not restored to %s: %#v", wantToken, current)
						}
					}
				}
			})
		}
	}
}
