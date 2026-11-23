package auth

import "time"

// clearStaleDisabledAvailability drops the undated aggregate block left by
// disabling a credential and unregistering its models. Only a transition
// back to active with no independent failure or cooldown is eligible.
func clearStaleDisabledAvailability(previous, auth *Auth, now time.Time) {
	if previous == nil || !isAuthDisabled(previous) || isAuthDisabled(auth) ||
		auth.Status != StatusActive || !auth.Unavailable || len(auth.ModelStates) != 0 {
		return
	}
	if auth.LastError != nil || auth.RefreshError != nil || auth.RequiresSignIn ||
		auth.RejectedAccessToken != "" || auth.RejectedSIWCAccessToken != "" ||
		auth.RejectedRefreshToken != "" || auth.RejectedSIWCRefreshToken != "" ||
		!auth.NextRetryAfter.IsZero() ||
		auth.Quota.Exceeded || !auth.Quota.NextRecoverAt.IsZero() ||
		effectiveAuthForceUntil(auth).After(now) || auth.credentialRetryAfter.After(now) {
		return
	}
	auth.Unavailable = false
}
