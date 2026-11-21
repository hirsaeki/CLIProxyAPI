package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func (s *FileTokenStore) SupportsManagedFileAuthFork() bool { return s != nil }

// patchManagedVertexModeFork runs under FileTokenStore's normal save lock.
// Update only this top-level policy field, using the latest on-disk document
// rather than serializing potentially older runtime metadata or token storage.
func patchManagedVertexModeFork(ctx context.Context, path string, auth *cliproxyauth.Auth) (string, error) {
	invalid := errors.New("managed Vertex credential changed or is not a regular local file")
	if auth == nil || auth.Provider != "vertex" {
		return "", invalid
	}
	mode, ok := auth.Metadata[vertexlocations.ModeKeyFork].(string)
	if !ok || (mode != vertexlocations.ModeAutomaticFork && mode != vertexlocations.ModeOffFork) {
		return "", invalid
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", invalid
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var disk map[string]any
	if err = json.Unmarshal(original, &disk); err != nil {
		return "", invalid
	}
	provider, _ := disk["type"].(string)
	if !strings.EqualFold(strings.TrimSpace(provider), "vertex") {
		return "", invalid
	}
	modeFields := 0
	ojson.ParseBytes(original).ForEach(func(key, _ ojson.Result) bool {
		if key.String() == vertexlocations.ModeKeyFork {
			modeFields++
		}
		return modeFields < 2
	})
	if modeFields > 1 {
		return "", invalid
	}
	for _, key := range []string{"service_account", "location", "project_id", "project", "access_token", "api_key"} {
		if !reflect.DeepEqual(disk[key], auth.Metadata[key]) {
			return "", invalid
		}
	}
	doc := ojson.NewDoc(original)
	if err = doc.Set(vertexlocations.ModeKeyFork, mode); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vertex-policy-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	payload := doc.Bytes()
	n, errWrite := tmp.Write(payload)
	if errWrite == nil && n != len(payload) {
		errWrite = io.ErrShortWrite
	}
	errMode := tmp.Chmod(info.Mode().Perm())
	errSync := tmp.Sync()
	errClose := tmp.Close()
	if err = errors.Join(errWrite, errMode, errSync, errClose); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Reject an external rename or edit before publishing the replacement.
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) || info.Mode() != current.Mode() {
		return "", invalid
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, original) {
		return "", invalid
	}
	if err = os.Rename(name, path); err != nil {
		return "", err
	}
	return path, nil
}
