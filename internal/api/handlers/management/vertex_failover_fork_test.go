package management

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/vertex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	fileauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const testVertexFailoverCredentialFork = "vertex-synthetic.json"

type vertexFailoverStoreFork struct {
	*fileauth.FileTokenStore
	fail bool
}

func (s *vertexFailoverStoreFork) Save(ctx context.Context, auth *coreauth.Auth) (string, error) {
	if s.fail {
		return "", errors.New("synthetic store failure with sensitive detail")
	}
	return s.FileTokenStore.Save(ctx, auth)
}

func TestVertexFailoverCredentialPersistenceFork(t *testing.T) {
	dir := t.TempDir()
	store := &vertexFailoverStoreFork{FileTokenStore: fileauth.NewFileTokenStore()}
	store.SetBaseDir(dir)
	manager := coreauth.NewManager(store, nil, nil)
	ctx := context.Background()
	legacy := map[string]any{"gemini-4-test": []any{"global", "us"}}
	account := map[string]any{"project_id": "fixture", "private_key": "synthetic-key-never-expose"}
	record := &coreauth.Auth{
		ID: testVertexFailoverCredentialFork, FileName: testVertexFailoverCredentialFork, Provider: "vertex",
		Storage: &vertex.VertexCredentialStorage{ServiceAccount: account, ProjectID: "fixture", Location: "global", Type: "vertex"},
		Metadata: map[string]any{"type": "vertex", "service_account": account, "project_id": "fixture",
			"location": "global", "note": "keep", vertexlocations.PolicyKeyFork: legacy},
	}
	if _, err := manager.Register(ctx, record); err != nil {
		t.Fatal(err)
	}
	call := func(m *coreauth.Manager, method, query, raw string, status int, mode string, explicit bool) string {
		t.Helper()
		h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: dir}, m)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, "/v8/management/credentials/vertex-failover"+query, strings.NewReader(raw))
		if method == http.MethodGet {
			h.GetVertexFailoverFork(c)
		} else {
			h.PatchVertexFailoverFork(c)
		}
		if rec.Code != status || strings.Contains(rec.Body.String(), "synthetic-") || strings.Contains(rec.Body.String(), "sensitive detail") {
			t.Fatalf("unexpected response %d %s", rec.Code, rec.Body.String())
		}
		if status == 200 {
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid response %s", rec.Body.String())
			}
			revision, _ := body["revision"].(string)
			if len(body) != 3 || body["mode"] != mode || body["explicit"] != explicit ||
				!validVertexFailoverRevisionFork(revision) || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("non-minimal or incorrect response %s", rec.Body.String())
			}
			return revision
		}
		return ""
	}

	query := "?name=" + testVertexFailoverCredentialFork
	var revision string
	patch := func(mode string) string {
		return `{"name":"` + testVertexFailoverCredentialFork + `","mode":"` + mode + `","revision":"` + revision + `"}`
	}
	path := filepath.Join(dir, testVertexFailoverCredentialFork)
	// An unrelated on-disk edit may exist before the watcher has caught up.
	// The policy mutation must not rewrite it from the runtime snapshot.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"note": "keep"`, `"note": "keep", "external":{"count":9007199254740993}`, 1))
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	revision = call(manager, http.MethodGet, query, "", 200, "legacy", false)
	call(manager, http.MethodGet, query+"&auth_index=wrong", "", 404, "", false)
	call(manager, http.MethodPatch, "", patch("legacy"), 400, "", false)
	call(manager, http.MethodPatch, "", `{"name":"`+testVertexFailoverCredentialFork+`","mode":"automatic"}`, 400, "", false)
	call(manager, http.MethodPatch, "", strings.TrimSuffix(patch("automatic"), "}")+`,"location":"eu"}`, 400, "", false)
	for _, mode := range []string{"automatic", "off"} {
		stalePatch := patch("off")
		revision = call(manager, http.MethodPatch, "", patch(mode), 200, mode, true)
		call(manager, http.MethodPatch, "", stalePatch, 409, "", false)
		if got := call(manager, http.MethodGet, query, "", 200, mode, true); got != revision {
			t.Fatal("successful PATCH and following GET disagree on revision")
		}
		raw, errRead := os.ReadFile(path)
		var saved map[string]any
		if errRead != nil || json.Unmarshal(raw, &saved) != nil ||
			!strings.Contains(string(raw), "9007199254740993") || saved[vertexlocations.ModeKeyFork] != mode || saved["location"] != "global" || saved["note"] != "keep" ||
			!reflect.DeepEqual(saved["service_account"], account) || !reflect.DeepEqual(saved[vertexlocations.PolicyKeyFork], legacy) {
			t.Fatal("file did not preserve credential, location, mode and legacy mapping")
		}
		restarted := coreauth.NewManager(store, nil, nil)
		if err := restarted.Load(ctx); err != nil {
			t.Fatal(err)
		}
		call(restarted, http.MethodGet, query, "", 200, mode, true)

		store.fail = true
		next := "automatic"
		if mode == next {
			next = "off"
		}
		call(manager, http.MethodPatch, "", patch(next), 500, "", false)
		call(manager, http.MethodGet, query, "", 200, mode, true)
		store.fail = false
	}
	// A different service account can be reuploaded under the same name/index.
	stale := patch("automatic")
	before, _ := manager.GetByID(testVertexFailoverCredentialFork)
	replacement := before.Clone()
	replacement.Metadata = map[string]any{"type": "vertex", "location": "global", "project_id": "replacement",
		"service_account":           map[string]any{"private_key": "synthetic-replacement", "project_id": "replacement"},
		vertexlocations.ModeKeyFork: "off"}
	replacement.Storage = nil
	if _, err := manager.Register(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	after, _ := manager.GetByID(testVertexFailoverCredentialFork)
	if lockedAuthIndex(after) != lockedAuthIndex(before) {
		t.Fatal("fixture must preserve auth index across replacement")
	}
	call(manager, http.MethodPatch, "", stale, 409, "", false)
	revision = call(manager, http.MethodGet, query, "", 200, "off", true)

	// Reject an on-disk policy revision that the watcher has not adopted yet.
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	staleDisk := strings.Replace(string(raw), `"vertex_region_failover_mode":"off"`, `"vertex_region_failover_mode":"automatic"`, 1)
	if staleDisk == string(raw) {
		t.Fatal("fixture did not change the on-disk policy")
	}
	if err = os.WriteFile(path, []byte(staleDisk), 0600); err != nil {
		t.Fatal(err)
	}
	call(manager, http.MethodPatch, "", patch("off"), 409, "", false)
	call(manager, http.MethodGet, query, "", 200, "off", true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	call(manager, http.MethodPatch, "", patch("automatic"), 500, "", false)
	call(manager, http.MethodGet, query, "", 200, "off", true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("mode patch must not recreate a deleted credential")
	}
}

func TestVertexFailoverCredentialEligibilityFork(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	for _, record := range []*coreauth.Auth{
		{ID: "gemini.json", FileName: "gemini.json", Provider: "gemini-cli", Metadata: map[string]any{"service_account": map[string]any{"fixture": true}}},
		{ID: "runtime.json", FileName: "runtime.json", Provider: "vertex", Attributes: map[string]string{coreauth.AttributeRuntimeOnly: "true"}, Metadata: map[string]any{"service_account": map[string]any{"fixture": true}}},
		{ID: "key.json", FileName: "key.json", Provider: "vertex", Attributes: map[string]string{coreauth.AttributeAPIKey: "synthetic"}, Metadata: map[string]any{"service_account": map[string]any{"fixture": true}}},
	} {
		if _, err := manager.Register(context.Background(), record); err != nil {
			t.Fatal(err)
		}
		h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
		res := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(res)
		c.Request = httptest.NewRequest(http.MethodGet, "/v8/management/credentials/vertex-failover?name="+record.ID, nil)
		h.GetVertexFailoverFork(c)
		if res.Code != http.StatusConflict {
			t.Fatalf("%s status=%d", record.ID, res.Code)
		}
	}
}
