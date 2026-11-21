package vertexlocations

import (
	"slices"
	"testing"
)

func TestCredentialModeAndAutomaticCandidatesFork(t *testing.T) {
	legacy := map[string]any{"gemini-example": []any{"eu", "us"}}
	for _, tc := range []struct {
		meta     map[string]any
		mode     string
		explicit bool
	}{
		{nil, ModeOffFork, false},
		{map[string]any{PolicyKeyFork: legacy}, ModeLegacyFork, false},
		{map[string]any{PolicyKeyFork: legacy, ModeKeyFork: ModeOffFork}, ModeOffFork, true},
		{map[string]any{PolicyKeyFork: legacy, ModeKeyFork: ModeAutomaticFork}, ModeAutomaticFork, true},
		{map[string]any{PolicyKeyFork: legacy, ModeKeyFork: "auto"}, ModeOffFork, true},
		{map[string]any{PolicyKeyFork: legacy, ModeKeyFork: nil}, ModeOffFork, true},
	} {
		if mode, explicit := CredentialModeFork(tc.meta); mode != tc.mode || explicit != tc.explicit {
			t.Fatalf("got mode=%q explicit=%t; want %q, %t", mode, explicit, tc.mode, tc.explicit)
		}
	}
	candidates := AutomaticCandidatesFork("eu")
	if len(candidates) != len(knownRegionsFork) || !slices.Equal(candidates[:4], []string{"eu", "global", "us", "us-west1"}) {
		t.Fatalf("unexpected fixed order or incomplete safe set: %v", candidates)
	}
	seen := make(map[string]bool)
	for _, location := range candidates {
		if !KnownLocation(location) || seen[location] {
			t.Fatalf("unsafe or duplicate candidate %q", location)
		}
		seen[location] = true
	}
	if AutomaticCandidatesFork("unknown") != nil {
		t.Fatal("unknown native primary must keep native path")
	}
}
