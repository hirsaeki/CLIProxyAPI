package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// These tests exercise level normalization and translation, not the rolling
// catalog. Keep their required capabilities available when a model is retired.
func registerThinkingLevelModelFixtureFork(t *testing.T, provider, modelID string, minBudget int) {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	clientID := t.Name() + "-thinking-level-fixture"
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{{
		ID: modelID,
		Thinking: &registry.ThinkingSupport{
			Min:            minBudget,
			Max:            65535,
			DynamicAllowed: true,
			Levels:         []string{"low", "medium", "high"},
		},
	}})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })
}
