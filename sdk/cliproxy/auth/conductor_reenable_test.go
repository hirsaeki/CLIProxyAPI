package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestClearStaleDisabledAvailability(t *testing.T) {
	now := time.Unix(1730000000, 0)
	future := now.Add(time.Minute)
	tests := []struct {
		name            string
		previous        Auth
		update          func(*Auth)
		wantUnavailable bool
	}{
		{
			name:     "disabled flag and status",
			previous: Auth{Disabled: true, Status: StatusDisabled},
		},
		{
			name:     "disabled status only",
			previous: Auth{Status: StatusDisabled},
			update:   func(a *Auth) { a.NextRefreshAfter = future },
		},
		{
			name:            "already enabled",
			previous:        Auth{Status: StatusActive, Unavailable: true},
			wantUnavailable: true,
		},
		{
			name:            "still disabled",
			previous:        Auth{Disabled: true},
			update:          func(a *Auth) { a.Disabled = true },
			wantUnavailable: true,
		},
		{
			name:            "independent error",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.Status = StatusError },
			wantUnavailable: true,
		},
		{
			name:            "unauthorized",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.LastError = &Error{HTTPStatus: http.StatusUnauthorized} },
			wantUnavailable: true,
		},
		{
			name:            "requires sign in",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.RequiresSignIn = true },
			wantUnavailable: true,
		},
		{
			name:            "rejected token",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.RejectedAccessToken = "rejected" },
			wantUnavailable: true,
		},
		{
			name:            "auth retry",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.NextRetryAfter = future },
			wantUnavailable: true,
		},
		{
			name:            "quota",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.Quota = QuotaState{Exceeded: true, NextRecoverAt: future} },
			wantUnavailable: true,
		},
		{
			name:            "credential scoped retry",
			previous:        Auth{Status: StatusDisabled},
			update:          func(a *Auth) { a.credentialRetryAfter = future },
			wantUnavailable: true,
		},
		{
			name:     "model scoped cooldown",
			previous: Auth{Status: StatusDisabled},
			update: func(a *Auth) {
				a.ModelStates = map[string]*ModelState{"m": {Unavailable: true, NextRetryAfter: future}}
			},
			wantUnavailable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Auth{Status: StatusActive, Unavailable: true}
			if tt.update != nil {
				tt.update(a)
			}
			clearStaleDisabledAvailability(&tt.previous, a, now)
			if a.Unavailable != tt.wantUnavailable {
				t.Fatalf("Unavailable = %v, want %v", a.Unavailable, tt.wantUnavailable)
			}
		})
	}
}
