package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
)

func TestManagerSourceFileSnapshotRejectsStaleFork(t *testing.T) {
	for _, operation := range []string{"Update", "RegisterExisting", "RegisterNew"} {
		for _, change := range []string{"changed", "missing", "unreadable"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "auth.json")
				raw := []byte(`{"type":"audit","access_token":"old"}`)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				saves := 0
				store := &callbackAuthStore{save: func(*Auth) error { saves++; return nil }}
				hook := &cancellationHook{}
				manager := NewManager(store, nil, hook)
				input := &Auth{ID: "source-snapshot", Provider: "audit", Status: StatusActive, Label: "stale", Metadata: map[string]any{"type": "audit", "access_token": "old"}}
				var before *Auth
				if operation != "RegisterNew" {
					current := input.Clone()
					current.Label = "current"
					current.Metadata["access_token"] = "current"
					var err error
					before, err = manager.Register(WithSkipPersist(ctx), current)
					if err != nil {
						t.Fatal(err)
					}
				}
				hook.calls.Store(0)
				epoch := manager.structuralEpoch.Load()
				changes, stop := manager.WatchAuthChanges(input.ID)
				defer stop()
				input.SetSourceFileSnapshot(path, raw)
				switch change {
				case "changed":
					if err := os.WriteFile(path, []byte(`{"type":"audit","access_token":"new"}`), 0600); err != nil {
						t.Fatal(err)
					}
				case "missing", "unreadable":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if change == "unreadable" {
						// A directory fails ReadFile even under root and on Windows.
						if err := os.Mkdir(path, 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				var returned *Auth
				var err error
				if operation == "Update" {
					returned, err = manager.Update(ctx, input)
				} else {
					returned, err = manager.Register(ctx, input)
				}
				if err != nil {
					t.Fatal(err)
				}
				got, exists := manager.GetByID(input.ID)
				if exists != (before != nil) || !reflect.DeepEqual(got, before) || !reflect.DeepEqual(returned, before) {
					t.Fatal("stale source snapshot changed the auth or failed to return the current snapshot")
				}
				if saves != 0 || hook.calls.Load() != 0 || manager.structuralEpoch.Load() != epoch {
					t.Fatal("stale source snapshot invoked persistence or post-commit side effects")
				}
				select {
				case <-changes:
					t.Fatal("stale source snapshot notified auth watchers")
				default:
				}
			})
		}
	}
}

func TestManagerSourceFileSnapshotAppliesExternalEditAfterAccountingFork(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := []byte(`{"type":"audit","access_token":"external","disabled":true}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	saves := 0
	manager := NewManager(&callbackAuthStore{save: func(*Auth) error { saves++; return nil }}, nil, nil)
	base, err := manager.Register(WithSkipPersist(ctx), &Auth{ID: "source-snapshot", Provider: "audit", Status: StatusActive, Metadata: map[string]any{"type": "audit", "access_token": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	external := &Auth{ID: base.ID, Provider: base.Provider, Status: StatusDisabled, Disabled: true, Metadata: map[string]any{"type": "audit", "access_token": "external", "disabled": true}}
	external.SetSourceFileSnapshot(path, raw)
	manager.MarkResult(WithSkipPersist(ctx), Result{AuthID: base.ID, Provider: base.Provider, Success: true})
	accounted, _ := manager.GetByID(base.ID)
	if accounted.Generation <= base.Generation || accounted.Success != 1 {
		t.Fatal("request accounting did not advance the runtime generation")
	}
	updated, err := manager.Update(ctx, external)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || !updated.Disabled || updated.Metadata["access_token"] != "external" || updated.Generation <= accounted.Generation || updated.Success != 1 || saves != 1 {
		t.Fatal("matching external edit was lost after request accounting")
	}
	if got, _ := manager.GetByID(base.ID); !reflect.DeepEqual(got, updated) {
		t.Fatal("returned external edit differs from the published snapshot")
	}
}

func TestAuthSourceFileSnapshotIsImmutableRuntimeStateFork(t *testing.T) {
	ctx := WithSkipPersist(context.Background())
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := []byte(`{"type":"audit"}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	input := &Auth{ID: "source-snapshot", Provider: "audit", Status: StatusActive, Metadata: map[string]any{"type": "audit"}}
	plain, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	input.SetSourceFileSnapshot(path, raw)
	clone := input.Clone()
	marked, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, marked) {
		t.Fatal("source snapshot marker leaked into serialized auth state")
	}
	// Neither the caller's byte slice nor replacing the original marker may
	// change the immutable snapshot retained by a clone.
	raw[0] = 'x'
	input.SetSourceFileSnapshot(path+".missing", raw)
	manager := NewManager(nil, nil, nil)
	if got, errRegister := manager.Register(ctx, clone.Clone()); errRegister != nil || got == nil {
		t.Fatalf("clone lost its original source snapshot: %v", errRegister)
	}
	if errRemove := os.Remove(path); errRemove != nil {
		t.Fatal(errRemove)
	}
	manager = NewManager(nil, nil, nil)
	if got, errRegister := manager.Register(ctx, clone.Clone()); errRegister != nil || got != nil {
		t.Fatalf("clone did not retain its source validation marker: %v", errRegister)
	}
}

func TestManagerSourceFileSnapshotLeavesVersionedMutationsUnchangedFork(t *testing.T) {
	for _, operation := range []string{"Update", "Register"} {
		t.Run(operation, func(t *testing.T) {
			ctx := WithSkipPersist(context.Background())
			manager := NewManager(nil, nil, nil)
			base, err := manager.Register(ctx, &Auth{ID: "source-snapshot", Provider: "audit", Status: StatusActive})
			if err != nil {
				t.Fatal(err)
			}
			input := base.Clone()
			input.Label = "versioned"
			input.SetSourceFileSnapshot(filepath.Join(t.TempDir(), "missing.json"), []byte(`{"type":"audit"}`))
			var got *Auth
			if operation == "Update" {
				got, err = manager.Update(ctx, input)
			} else {
				got, err = manager.Register(ctx, input)
			}
			if err != nil || got == nil || got.Label != "versioned" {
				t.Fatalf("source guard rejected a versioned mutation: %v", err)
			}
		})
	}
}

func TestManagerSourceFileSnapshotValidatedAfterActiveSaveFork(t *testing.T) {
	for _, operation := range []string{"Update", "Register"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "auth.json")
				oldRaw := []byte(`{"type":"audit","access_token":"old"}`)
				newRaw := []byte(`{"type":"audit","access_token":"new"}`)
				if err := os.WriteFile(path, oldRaw, 0600); err != nil {
					t.Fatal(err)
				}
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				defer unblock()
				saves := 0
				var writeErr error
				store := &callbackAuthStore{save: func(auth *Auth) error {
					saves++
					if auth.Label == "newer" {
						close(entered)
						<-release
						writeErr = os.WriteFile(path, newRaw, 0600)
						return writeErr
					}
					return nil
				}}
				hook := &cancellationHook{}
				manager := NewManager(store, nil, hook)
				base, err := manager.Register(WithSkipPersist(ctx), &Auth{ID: "source-snapshot", Provider: "audit", Status: StatusActive, Metadata: map[string]any{"type": "audit", "access_token": "old"}})
				if err != nil {
					t.Fatal(err)
				}
				hook.calls.Store(0)
				epoch := manager.structuralEpoch.Load()
				stale := &Auth{ID: base.ID, Provider: base.Provider, Status: StatusActive, Label: "stale", Metadata: map[string]any{"type": "audit", "access_token": "old"}}
				stale.SetSourceFileSnapshot(path, oldRaw)
				newer := base.Clone()
				newer.Label = "newer"
				newer.Metadata["access_token"] = "new"
				var saved, returned *Auth
				writerDone := make(chan error, 1)
				go func() {
					var errUpdate error
					saved, errUpdate = manager.Update(ctx, newer)
					writerDone <- errUpdate
				}()
				<-entered
				staleDone := make(chan error, 1)
				go func() {
					var errMutation error
					if operation == "Update" {
						returned, errMutation = manager.Update(ctx, stale)
					} else {
						returned, errMutation = manager.Register(ctx, stale)
					}
					staleDone <- errMutation
				}()
				synctest.Wait()
				select {
				case <-staleDone:
					t.Fatal("file snapshot mutation overtook the active save")
				default:
				}
				unblock()
				if errWriter := <-writerDone; errWriter != nil {
					t.Fatal(errWriter)
				}
				if errStale := <-staleDone; errStale != nil {
					t.Fatal(errStale)
				}
				if writeErr != nil {
					t.Fatal(writeErr)
				}
				got, _ := manager.GetByID(base.ID)
				if saved == nil || saved.Label != "newer" || !reflect.DeepEqual(got, saved) || !reflect.DeepEqual(returned, saved) {
					t.Fatal("source was checked before the gate, allowing a stale snapshot to undo a completed save")
				}
				if saves != 1 || hook.calls.Load() != 1 || manager.structuralEpoch.Load() != epoch+1 {
					t.Fatal("discarded file snapshot caused an extra save or post-commit side effect")
				}
			})
		})
	}
}

// Register must use the same canonical observation as upstream Update: a
// formatting-only rewrite is current, while actual content changes remain stale.
func TestManagerSourceFileSnapshotRegisterAcceptsReformatFork(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			ctx := WithSkipPersist(t.Context())
			manager := NewManager(nil, nil, nil)
			input := &Auth{ID: t.Name(), Provider: "audit", Status: StatusActive, Label: "fresh"}
			if existing {
				old := input.Clone()
				old.Label = "old"
				if _, err := manager.Register(ctx, old); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "auth.json")
			input.SetSourceFileSnapshot(path, []byte(`{"type":"audit","label":"fresh"}`))
			if err := os.WriteFile(path, []byte("{\n  \"label\": \"fresh\",\n  \"type\": \"audit\"\n}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := manager.Register(ctx, input)
			if err != nil || got == nil || got.Label != "fresh" || got.Generation == 0 {
				t.Fatalf("canonical source was not registered: auth=%#v, error=%v", got, err)
			}
			if got.fileSnapshotPath != "" || got.fileSnapshotDigest != "" {
				t.Fatal("committed auth retained its file observation")
			}
		})
	}
}
