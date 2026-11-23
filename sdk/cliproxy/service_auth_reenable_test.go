package cliproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"golang.org/x/crypto/bcrypt"
)

type reenableVertexExecutor struct{ syncTestExecutor }

func (*reenableVertexExecutor) Identifier() string { return "vertex" }

func (*reenableVertexExecutor) ExecuteStream(_ context.Context, a *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte(a.ID)}
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
}

func TestVertexStatusPatchRestoresStreamSelection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	const file = "vertex-reenable-stream.json"
	const model = "gemini-4-hydrogen"
	const managementKey = "test-secret"
	path := filepath.Join(dir, file)
	if errWrite := os.WriteFile(path, []byte(`{"type":"vertex","location":"global","disabled":false}`), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	t.Cleanup(func() { GlobalModelRegistry().UnregisterClient(file) })
	hash, errHash := bcrypt.GenerateFromPassword([]byte(managementKey), bcrypt.MinCost)
	if errHash != nil {
		t.Fatal(errHash)
	}
	cfg := &config.Config{AuthDir: dir, RemoteManagement: config.RemoteManagement{
		SecretKey: string(hash), DisableControlPanel: true,
	}}
	store := sdkauth.NewFileTokenStore()
	store.SetBaseDir(dir)
	manager := coreauth.NewManager(store, nil, nil)
	configPath := filepath.Join(dir, "config.yaml")
	service, errBuild := NewBuilder().WithConfig(cfg).WithConfigPath(configPath).WithCoreAuthManager(manager).Build()
	if errBuild != nil {
		t.Fatal(errBuild)
	}
	auth := &coreauth.Auth{ID: file, FileName: file, Provider: "vertex", Status: coreauth.StatusActive,
		Attributes: map[string]string{"path": path},
		Metadata:   map[string]any{"type": "vertex", "location": "global", "disabled": false},
	}
	if errSync := service.runtimeAuthSyncHook()(ctx, auth); errSync != nil {
		t.Fatal(errSync)
	}
	handler := api.NewServer(cfg, manager, service.accessManager, configPath, service.serverOptions...).Handler()
	patch := func(disabled bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/v8/management/credentials/status", strings.NewReader(`{"name":"`+file+`","disabled":`+strconv.FormatBool(disabled)+`}`))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer "+managementKey)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status PATCH disabled=%t: %d %s", disabled, res.Code, res.Body)
		}
		body, errRead := os.ReadFile(path)
		if errRead != nil {
			t.Fatal(errRead)
		}
		var record struct {
			Disabled *bool `json:"disabled"`
		}
		if errDecode := json.Unmarshal(body, &record); errDecode != nil || record.Disabled == nil || *record.Disabled != disabled {
			t.Fatalf("disabled=%t was not persisted: %s (%v)", disabled, body, errDecode)
		}
	}
	stream := func() {
		t.Helper()
		// Replace the real executor after each auth sync so no external requests
		// occur, while leaving the model registry and scheduler intact.
		manager.RegisterExecutor(&reenableVertexExecutor{})
		res, errExecute := manager.ExecuteStream(ctx, []string{"vertex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
		if errExecute != nil {
			t.Fatalf("select %s auth for stream: %v", model, errExecute)
		}
		payload := ""
		for chunk := range res.Chunks {
			if chunk.Err != nil {
				t.Fatal(chunk.Err)
			}
			payload += string(chunk.Payload)
		}
		if payload != file {
			t.Fatalf("unexpected stream payload %q", payload)
		}
	}
	stream()
	active, ok := manager.GetByID(file)
	if !ok || len(active.ModelStates) == 0 {
		t.Fatal("the first successful stream did not establish model state")
	}
	patch(true)
	disabled, ok := manager.GetByID(file)
	if !ok || !disabled.Disabled || len(disabled.ModelStates) != 0 {
		t.Fatal("disable did not remove the runtime model state")
	}
	if GlobalModelRegistry().ClientSupportsModel(file, model) {
		t.Fatal("disabled credential still has a registered route")
	}
	patch(false)
	if !GlobalModelRegistry().ClientSupportsModel(file, model) {
		t.Fatal("enabled credential did not regain its registered route")
	}
	stream()
}
