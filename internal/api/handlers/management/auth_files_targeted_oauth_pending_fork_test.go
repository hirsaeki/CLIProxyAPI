package management

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/auth/kimi"
	metaauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/meta"
	xaiauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/xai"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// pendingDeviceOAuthWaiterFork keeps registration fixtures pending without HTTP.
// The subtest cancels its OAuth session before cleanup joins the polling call.
type pendingDeviceOAuthWaiterFork struct {
	done chan struct{}
}

func (w *pendingDeviceOAuthWaiterFork) wait(ctx context.Context) error {
	defer close(w.done)
	<-ctx.Done()
	return ctx.Err()
}

type pendingKimiOAuthServiceFork struct {
	targetedTestKimiOAuthService
	*pendingDeviceOAuthWaiterFork
}

func (s *pendingKimiOAuthServiceFork) WaitForAuthorization(ctx context.Context, _ *kimi.DeviceCodeResponse) (*kimi.KimiAuthBundle, error) {
	return nil, s.wait(ctx)
}

type pendingXAIOAuthServiceFork struct {
	targetedTestXAIOAuthService
	*pendingDeviceOAuthWaiterFork
}

func (s *pendingXAIOAuthServiceFork) WaitForAuthorization(ctx context.Context, _ *xaiauth.DeviceCodeResponse) (*xaiauth.AuthBundle, error) {
	return nil, s.wait(ctx)
}

type pendingMetaOAuthServiceFork struct {
	targetedTestMetaOAuthService
	*pendingDeviceOAuthWaiterFork
}

func (s *pendingMetaOAuthServiceFork) WaitForAuthorization(ctx context.Context, _ *metaauth.DeviceCodeResponse) (*metaauth.MetaAuthBundle, error) {
	return nil, s.wait(ctx)
}

func installPendingDeviceOAuthFixtureFork(t *testing.T, provider string) {
	t.Helper()
	waiter := &pendingDeviceOAuthWaiterFork{done: make(chan struct{})}
	switch provider {
	case "kimi", "kimi-ai":
		original := newKimiOAuthService
		newKimiOAuthService = func(_ *config.Config, domain string) kimiOAuthService {
			return &pendingKimiOAuthServiceFork{
				targetedTestKimiOAuthService: targetedTestKimiOAuthService{domain: domain},
				pendingDeviceOAuthWaiterFork: waiter,
			}
		}
		t.Cleanup(func() { newKimiOAuthService = original })
	case "xai":
		original := newXAIOAuthService
		newXAIOAuthService = func(*config.Config, string) xaiOAuthService {
			return &pendingXAIOAuthServiceFork{pendingDeviceOAuthWaiterFork: waiter}
		}
		t.Cleanup(func() { newXAIOAuthService = original })
	case "meta":
		original := newMetaOAuthService
		newMetaOAuthService = func(*config.Config, string) metaOAuthService {
			return &pendingMetaOAuthServiceFork{pendingDeviceOAuthWaiterFork: waiter}
		}
		t.Cleanup(func() { newMetaOAuthService = original })
	default:
		return
	}
	// Registered before the test's session cancellation cleanup: LIFO cleanup
	// cancels the session, joins polling, then restores the service factory.
	t.Cleanup(func() {
		select {
		case <-waiter.done:
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for canceled device OAuth fixture")
		}
	})
}
