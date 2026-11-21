package vertexlocations

const (
	PolicyKeyFork     = "vertex_region_failover"
	ModeKeyFork       = "vertex_region_failover_mode"
	ModeOffFork       = "off"
	ModeAutomaticFork = "automatic"
	ModeLegacyFork    = "legacy"
)

// CredentialModeFork distinguishes old policies from an explicitly saved mode.
// Invalid explicit values fail closed rather than reactivating a legacy policy.
func CredentialModeFork(metadata map[string]any) (mode string, explicit bool) {
	raw, present := metadata[ModeKeyFork]
	if present {
		if value, ok := raw.(string); ok && value == ModeAutomaticFork {
			return ModeAutomaticFork, true
		}
		return ModeOffFork, true
	}
	if legacy, ok := metadata[PolicyKeyFork].(map[string]any); ok && len(legacy) != 0 {
		return ModeLegacyFork, false
	}
	return ModeOffFork, false
}

// AutomaticCandidatesFork returns a primary-first plan in the fixed order of
// the built-in groups and their columns. Secondary support is checked separately
// against a fresh, exact-model snapshot; this is only a list of safe addresses.
func AutomaticCandidatesFork(primary string) []string {
	if !KnownLocation(primary) {
		return nil
	}
	out := make([]string, 1, len(knownRegionsFork))
	out[0] = primary
	for _, group := range regionGroupsFork {
		for _, location := range group {
			if location != primary {
				out = append(out, location)
			}
		}
	}
	return out
}
