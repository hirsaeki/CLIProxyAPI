package cliproxy

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestForkConfigCommitKeepsAvailabilityWarningAndCancelsStaleProbe(t *testing.T) {
	_, hook := logtest.NewNullLogger()
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	oldLevel := logger.GetLevel()
	logger.AddHook(hook)
	logger.SetLevel(log.WarnLevel)
	t.Cleanup(func() {
		logger.SetLevel(oldLevel)
		logger.ReplaceHooks(oldHooks)
	})

	snapshot := &oauthModelAvailabilitySnapshot{}
	svc := &Service{
		cfg:                        &config.Config{OAuthModelAvailabilityFile: "loaded.json"},
		oauthModelAvailability:     snapshot,
		oauthModelAvailabilityPath: "loaded.json",
	}
	auth := antigravityTestAuth("fork-config-probe", "https://probe.invalid")
	token := auth.Metadata["access_token"].(string)
	ctx, finish, active := svc.beginAntigravityAccountProbe(t.Context(), auth, svc.antigravityCapabilityKey(auth), "fork-config-flight", token)
	defer finish()
	if !active || ctx.Err() != nil {
		t.Fatal("initial probe was not active")
	}

	next := &config.Config{OAuthModelAvailabilityFile: "new.json"}
	next.ProxyURL = "direct"
	commit := svc.commitConfigUpdate(next)
	if commit.cfg != next || commit.sequence != 1 || svc.cfg != next {
		t.Fatalf("config was not committed: %#v", commit)
	}
	if ctx.Err() == nil {
		t.Fatal("config change did not cancel the stale Antigravity probe")
	}
	if svc.oauthModelAvailability != snapshot || svc.oauthModelAvailabilityPath != "loaded.json" {
		t.Fatal("config reload replaced the startup-only availability snapshot")
	}
	entries := hook.AllEntries()
	if len(entries) != 1 || entries[0].Message != "OAuth model availability path changed; restart required to load the new sidecar" || entries[0].Data["configured_path"] != "new.json" || entries[0].Data["loaded_path"] != "loaded.json" {
		t.Fatalf("availability warning was not preserved: %#v", entries)
	}
}
