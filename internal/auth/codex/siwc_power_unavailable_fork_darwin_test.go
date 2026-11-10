package codex

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestSIWCPowerStateUnavailableDefersWithoutRetriesFork(t *testing.T) {
	previous := ReadSIWCSystemState
	t.Cleanup(func() { ReadSIWCSystemState = previous })
	probeErr := errors.New("power-state probe unavailable")
	checks := 0
	ReadSIWCSystemState = func(context.Context) ([]byte, error) {
		checks++
		return nil, probeErr
	}
	auth := &CodexAuth{httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("token request sent without a known power state")
		return nil, nil
	})}}
	token, errRefresh := auth.RefreshSIWCTokensWithRetry(context.Background(), "refresh", "oaiapp_probe", "", 3)
	if token != nil || checks != 1 {
		t.Fatalf("token returned = %t, checks = %d; want no token and exactly one probe", token != nil, checks)
	}
	if !errors.Is(errRefresh, probeErr) || !errors.Is(errRefresh, ErrSIWCRefreshDeferred) {
		t.Fatalf("error lost probe cause or deferral sentinel: %v", errRefresh)
	}
	var partition *SIWCRefreshError
	if !errors.As(errRefresh, &partition) {
		t.Fatalf("error lost SIWC partition: %v", errRefresh)
	}
}
