//go:build windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type windowsCallSerializationState struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	workers    int
	first      chan struct{}
	concurrent chan struct{}
	release    chan struct{}
	firstOnce  sync.Once
	allOnce    sync.Once
}

var (
	windowsCallSerializationStateMu sync.Mutex
	windowsCallSerializationCurrent *windowsCallSerializationState
)

func windowsCallSerializationPluginCall(_ uintptr, _ uintptr, _ uintptr, _ uintptr) uintptr {
	windowsCallSerializationStateMu.Lock()
	state := windowsCallSerializationCurrent
	windowsCallSerializationStateMu.Unlock()
	if state == nil {
		return 1
	}

	state.mu.Lock()
	state.active++
	if state.active > state.maxActive {
		state.maxActive = state.active
	}
	state.firstOnce.Do(func() { close(state.first) })
	if state.active == state.workers {
		state.allOnce.Do(func() { close(state.concurrent) })
	}
	state.mu.Unlock()

	<-state.release

	state.mu.Lock()
	state.active--
	state.mu.Unlock()
	return 0
}

func TestDynamicLibraryClientSerializesCalls(t *testing.T) {
	const workers = 5
	state := &windowsCallSerializationState{
		workers:    workers,
		first:      make(chan struct{}),
		concurrent: make(chan struct{}),
		release:    make(chan struct{}),
	}
	windowsCallSerializationStateMu.Lock()
	if windowsCallSerializationCurrent != nil {
		windowsCallSerializationStateMu.Unlock()
		t.Fatal("windows call serialization test state is already active")
	}
	windowsCallSerializationCurrent = state
	windowsCallSerializationStateMu.Unlock()
	t.Cleanup(func() {
		windowsCallSerializationStateMu.Lock()
		windowsCallSerializationCurrent = nil
		windowsCallSerializationStateMu.Unlock()
	})

	client := &dynamicLibraryClient{api: windowsPluginAPI{
		call: syscall.NewCallback(windowsCallSerializationPluginCall),
	}}
	t.Cleanup(client.Shutdown)
	start := make(chan struct{})
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errCall := client.Call(context.Background(), "model.for_auth", nil)
			errCh <- errCall
		}()
	}
	close(start)

	select {
	case <-state.first:
	case <-time.After(5 * time.Second):
		close(state.release)
		t.Fatal("first plugin call did not start")
	}

	concurrent := false
	select {
	case <-state.concurrent:
		concurrent = true
	case <-time.After(250 * time.Millisecond):
	}
	close(state.release)
	wg.Wait()
	close(errCh)
	for errCall := range errCh {
		if errCall != nil {
			t.Fatalf("Call() error = %v", errCall)
		}
	}

	state.mu.Lock()
	maxActive := state.maxActive
	state.mu.Unlock()
	if concurrent || maxActive != 1 {
		t.Fatalf("concurrent native plugin calls = %v, max active = %d, want serialized calls", concurrent, maxActive)
	}
}

func TestShadowPluginDirIsProcessScoped(t *testing.T) {
	dir, errDir := shadowPluginDir()
	if errDir != nil {
		t.Fatalf("shadowPluginDir() error = %v", errDir)
	}
	want := filepath.Join(os.TempDir(), "cliproxy-pluginhost", fmt.Sprintf("pid-%d", os.Getpid()))
	if dir != want {
		t.Fatalf("shadowPluginDir() = %q, want %q", dir, want)
	}
}

func TestShadowCopyPluginReusesContentAddressedShadow(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "alpha.dll")
	content := []byte("plugin-v1")
	if errWrite := os.WriteFile(source, content, 0o644); errWrite != nil {
		t.Fatalf("WriteFile() error = %v", errWrite)
	}
	file := pluginFile{ID: "alpha", Path: source}

	first, errFirst := shadowCopyPluginToDir(file, dir)
	if errFirst != nil {
		t.Fatalf("shadowCopyPluginToDir() first error = %v", errFirst)
	}
	second, errSecond := shadowCopyPluginToDir(file, dir)
	if errSecond != nil {
		t.Fatalf("shadowCopyPluginToDir() second error = %v", errSecond)
	}

	if second != first {
		t.Fatalf("second shadow path = %q, want reused path %q", second, first)
	}
	gotContent, errRead := os.ReadFile(first)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", first, errRead)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("shadow content = %q, want %q", gotContent, content)
	}
	digest := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(digest[:])[:shadowPluginDigestLength]
	name := filepath.Base(first)
	if !strings.HasPrefix(name, shadowPluginPrefix+"alpha-") || !strings.Contains(name, wantDigest) {
		t.Fatalf("shadow file name = %q, want alpha content digest %s", name, wantDigest)
	}
	if count := countShadowPluginFiles(t, dir); count != 1 {
		t.Fatalf("shadow file count = %d, want 1", count)
	}
}

func TestShadowCopyPluginCreatesNewPathForChangedContent(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "alpha.dll")
	file := pluginFile{ID: "alpha", Path: source}
	if errWrite := os.WriteFile(source, []byte("plugin-v1"), 0o644); errWrite != nil {
		t.Fatalf("WriteFile() v1 error = %v", errWrite)
	}
	first, errFirst := shadowCopyPluginToDir(file, dir)
	if errFirst != nil {
		t.Fatalf("shadowCopyPluginToDir() v1 error = %v", errFirst)
	}

	if errWrite := os.WriteFile(source, []byte("plugin-v2"), 0o644); errWrite != nil {
		t.Fatalf("WriteFile() v2 error = %v", errWrite)
	}
	second, errSecond := shadowCopyPluginToDir(file, dir)
	if errSecond != nil {
		t.Fatalf("shadowCopyPluginToDir() v2 error = %v", errSecond)
	}

	if second == first {
		t.Fatalf("second shadow path reused %q after content changed", second)
	}
	if count := countShadowPluginFiles(t, dir); count != 2 {
		t.Fatalf("shadow file count = %d, want 2 versions", count)
	}
}

func TestShadowCopyPluginReplacesCorruptSameSizeShadow(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "alpha.dll")
	content := []byte("plugin-v1")
	if errWrite := os.WriteFile(source, content, 0o644); errWrite != nil {
		t.Fatalf("WriteFile() source error = %v", errWrite)
	}
	digest := sha256.Sum256(content)
	target := shadowPluginPath(dir, "alpha", hex.EncodeToString(digest[:]), ".dll")
	if errWrite := os.WriteFile(target, []byte("corrupt!!"), 0o644); errWrite != nil {
		t.Fatalf("WriteFile() corrupt shadow error = %v", errWrite)
	}

	gotPath, errCopy := shadowCopyPluginToDir(pluginFile{ID: "alpha", Path: source}, dir)
	if errCopy != nil {
		t.Fatalf("shadowCopyPluginToDir() error = %v", errCopy)
	}

	if gotPath != target {
		t.Fatalf("shadow path = %q, want %q", gotPath, target)
	}
	gotContent, errRead := os.ReadFile(target)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", target, errRead)
	}
	if string(gotContent) != string(content) {
		t.Fatalf("shadow content = %q, want %q", gotContent, content)
	}
	if count := countShadowPluginFiles(t, dir); count != 1 {
		t.Fatalf("shadow file count = %d, want 1", count)
	}
}

func TestRemoveStaleShadowPluginsOnlyRemovesShadowFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, shadowPluginPrefix+"alpha-deadbeef.dll")
	temp := filepath.Join(dir, shadowPluginTempPrefix+"alpha-temp.dll")
	keep := filepath.Join(dir, "keep.dll")
	for _, path := range []string{stale, temp, keep} {
		if errWrite := os.WriteFile(path, []byte("x"), 0o644); errWrite != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, errWrite)
		}
	}

	removeStaleShadowPlugins(dir)

	for _, path := range []string{stale, temp} {
		if _, errStat := os.Stat(path); !os.IsNotExist(errStat) {
			t.Fatalf("Stat(%s) error = %v, want not exist", path, errStat)
		}
	}
	if _, errStat := os.Stat(keep); errStat != nil {
		t.Fatalf("Stat(%s) error = %v, want kept", keep, errStat)
	}
}

func countShadowPluginFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, errRead := os.ReadDir(dir)
	if errRead != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, errRead)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), shadowPluginPrefix) {
			count++
		}
		if strings.HasPrefix(entry.Name(), shadowPluginTempPrefix) {
			t.Fatalf("temporary shadow file was not cleaned up: %s", entry.Name())
		}
	}
	return count
}
