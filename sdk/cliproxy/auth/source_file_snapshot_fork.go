package auth

// SetSourceFileSnapshot preserves the fork API while using upstream's canonical
// file observation binding and runtime clearing semantics.
func (a *Auth) SetSourceFileSnapshot(path string, data []byte) {
	a.BindFileSnapshot(path, data)
}
