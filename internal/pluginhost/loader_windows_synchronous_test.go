//go:build windows

package pluginhost

import (
	"context"
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

	client := newSynchronousWindowsPluginClient(&dynamicLibraryClient{api: windowsPluginAPI{
		call: syscall.NewCallback(windowsCallSerializationPluginCall),
	}})
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
