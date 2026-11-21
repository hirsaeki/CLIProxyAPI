package auth

import (
	"context"
	"errors"
)

type authPersistenceKeyFork struct{}

// managedFileAuthStoreFork identifies stores with a local atomic policy write;
// mirrored stores can fail after updating their watcher-visible local copy.
type managedFileAuthStoreFork interface {
	Store
	SupportsManagedFileAuthFork() bool
}

// SupportsManagedFileAuthFork reports whether management can edit local auth policy.
func (m *Manager) SupportsManagedFileAuthFork() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	store, ok := m.store.(managedFileAuthStoreFork)
	m.mu.RUnlock()
	return ok && store.SupportsManagedFileAuthFork()
}

var errAuthNotPersistedFork = errors.New("managed auth was not persisted")

func requireAuthPersistenceFork(ctx context.Context) bool {
	return ctx != nil && ctx.Value(authPersistenceKeyFork{}) == true
}

// ModifyPersistedFork applies a management update under the normal per-auth
// mutation gate, but does not publish a modification whose store save failed.
func (m *Manager) ModifyPersistedFork(ctx context.Context, id string, epoch uint64, edit func(*Auth) error) (*Auth, bool, error) {
	return m.Modify(context.WithValue(ctx, authPersistenceKeyFork{}, true), id, epoch, edit)
}

// RequiresManagedFileAuthSaveFork tells the built-in file store to make the
// narrowly scoped atomic edit rather than rewriting the credential record.
func RequiresManagedFileAuthSaveFork(ctx context.Context) bool {
	return requireAuthPersistenceFork(ctx)
}

func skippedManagedAuthSaveFork(ctx context.Context) error {
	if requireAuthPersistenceFork(ctx) {
		return errAuthNotPersistedFork
	}
	return nil
}

func saveManagedAuthFork(ctx context.Context, store Store, auth *Auth) error {
	strict := requireAuthPersistenceFork(ctx)
	if strict {
		managed, ok := store.(managedFileAuthStoreFork)
		if !ok || !managed.SupportsManagedFileAuthFork() {
			return errAuthNotPersistedFork
		}
	}
	path, err := store.Save(ctx, auth)
	if err == nil && path == "" && strict {
		return errAuthNotPersistedFork
	}
	return err
}
