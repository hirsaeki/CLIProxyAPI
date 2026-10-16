package auth

import (
	"crypto/sha256"
	"os"
)

// sourceFileSnapshot records the exact bytes parsed into a watcher snapshot.
// It is immutable so Auth.Clone may safely share it without retaining credentials.
type sourceFileSnapshot struct {
	path   string
	digest [sha256.Size]byte
}

// SetSourceFileSnapshot attaches runtime-only provenance to file-synthesized auths.
// Versioned runtime mutations do not consult this marker.
func (a *Auth) SetSourceFileSnapshot(path string, data []byte) {
	if a == nil {
		return
	}
	a.sourceFileSnapshot = &sourceFileSnapshot{path: path, digest: sha256.Sum256(data)}
}

// sourceFileSnapshotCurrent must run under the per-auth mutation gate, outside
// Manager.mu. Managed persistence cannot change the file between this check and
// commit. External writers still rely on subsequent filesystem observations.
func (a *Auth) sourceFileSnapshotCurrent() bool {
	if a == nil || a.Generation != 0 || a.sourceFileSnapshot == nil {
		return true
	}
	snapshot := a.sourceFileSnapshot
	data, err := os.ReadFile(snapshot.path)
	return err == nil && sha256.Sum256(data) == snapshot.digest
}
