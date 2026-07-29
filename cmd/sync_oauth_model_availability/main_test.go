package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClaudeResult(t *testing.T) {
	result, err := parseClaudeResult([]byte(`{
  "client":{"name":"@anthropic-ai/claude-agent-sdk","version":"0.3.219","artifact_sha256":"abc"},
  "account":{"email":"user@example.com","api_provider":"firstParty"},
  "models":[{"value":"sonnet","resolved_model":"claude-sonnet-5","display_name":"Sonnet","description":"Fast","supported_effort_levels":["low","high"]}]
}`))
	if err != nil {
		t.Fatalf("parseClaudeResult: %v", err)
	}
	if result.Account.Email != "user@example.com" || len(result.Models) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Models[0].ID != "claude-sonnet-5" {
		t.Fatalf("model id = %q", result.Models[0].ID)
	}
	if len(result.Models[0].SupportedEffortLevels) != 2 {
		t.Fatalf("effort levels = %#v", result.Models[0].SupportedEffortLevels)
	}
}

func TestParseGrokCache(t *testing.T) {
	raw := []byte(`{
  "fetched_at":"2026-07-25T00:00:00Z",
  "grok_version":"0.3.0",
  "auth_method":"session",
  "models":{
    "grok-4":{"info":{"model":"grok-4","name":"Grok 4","description":"Reasoning","context_window":131072,"max_completion_tokens":8192,"hidden":false}},
    "hidden":{"info":{"model":"hidden","context_window":1,"hidden":true},"api_key":"must-not-leak"}
  }
}`)
	result, err := parseGrokCache(raw)
	if err != nil {
		t.Fatalf("parseGrokCache: %v", err)
	}
	if result.Version != "0.3.0" || len(result.Models) != 1 || result.Models[0].ID != "grok-4" {
		t.Fatalf("result = %#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("sanitized result leaked credentials: %s", encoded)
	}
}

func TestUpdateSidecarPreservesOtherCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "availability.json")
	initial := `{"schema_version":1,"generated_at":"2026-07-24T00:00:00Z","credentials":[{"provider":"xai","auth_id":"other","client":{"name":"grok","version":"1"},"models":[{"id":"grok-old"}]}]}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := sidecarCredential{Provider: "claude", AuthID: "target", Client: sidecarClient{Name: "sdk", Version: "1"}, Models: []sidecarModel{{ID: "claude-new"}}}
	added, removed, err := updateSidecar(path, entry)
	if err != nil {
		t.Fatalf("updateSidecar: %v", err)
	}
	if len(added) != 1 || added[0] != "claude-new" || len(removed) != 0 {
		t.Fatalf("added=%#v removed=%#v", added, removed)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document sidecarDocument
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Credentials) != 2 {
		t.Fatalf("credentials = %#v", document.Credentials)
	}
}

func TestVerifyIdentity(t *testing.T) {
	if err := verifyIdentity("user@example.com", "user@example.com", "", ""); err != nil {
		t.Fatalf("matching email: %v", err)
	}
	if err := verifyIdentity("user@example.com", "other@example.com", "", ""); err == nil {
		t.Fatal("mismatching email error = nil")
	}
	if err := verifyIdentity("", "", "subject-a", "subject-a"); err != nil {
		t.Fatalf("matching subject: %v", err)
	}
	if err := verifyIdentity("", "", "", ""); err == nil {
		t.Fatal("unverifiable identity error = nil")
	}
}

func TestExtractGrokIdentityRejectsAmbiguousSessions(t *testing.T) {
	email, subject := extractGrokIdentity([]byte(`{
  "scope-a":{"auth_mode":"oidc","email":"a@example.com","user_id":"a"},
  "scope-b":{"auth_mode":"external","email":"b@example.com","user_id":"b"},
  "xai::api_key":{"auth_mode":"api_key","key":"secret"}
}`))
	if email != "" || subject != "" {
		t.Fatalf("ambiguous identity = %q, %q; want empty", email, subject)
	}

	email, subject = extractGrokIdentity([]byte(`{
  "scope":{"auth_mode":"oidc","email":"a@example.com","user_id":"a"},
  "xai::api_key":{"auth_mode":"api_key","key":"secret"}
}`))
	if email != "a@example.com" || subject != "a" {
		t.Fatalf("identity = %q, %q", email, subject)
	}
}
