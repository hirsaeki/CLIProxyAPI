// Command sync_oauth_model_availability updates one credential entry in an
// OAuth model availability sidecar using an official provider client.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const claudePackage = "@anthropic-ai/claude-agent-sdk"

type sidecarDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Credentials   []sidecarCredential `json:"credentials"`
}

type sidecarCredential struct {
	Provider string         `json:"provider"`
	AuthID   string         `json:"auth_id"`
	Client   sidecarClient  `json:"client"`
	Models   []sidecarModel `json:"models"`
}

type sidecarClient struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
}

type sidecarModel struct {
	ID                        string   `json:"id"`
	DisplayName               string   `json:"display_name,omitempty"`
	Description               string   `json:"description,omitempty"`
	ContextLength             int      `json:"context_length,omitempty"`
	MaxCompletionTokens       int      `json:"max_completion_tokens,omitempty"`
	SupportedParameters       []string `json:"supported_parameters,omitempty"`
	SupportedInputModalities  []string `json:"supported_input_modalities,omitempty"`
	SupportedOutputModalities []string `json:"supported_output_modalities,omitempty"`
	SupportedEffortLevels     []string `json:"supported_effort_levels,omitempty"`
}

type claudeResult struct {
	Client  sidecarClient `json:"client"`
	Account struct {
		Email       string `json:"email"`
		APIProvider string `json:"api_provider"`
	} `json:"account"`
	Models []struct {
		Value                 string   `json:"value"`
		ResolvedModel         string   `json:"resolved_model"`
		DisplayName           string   `json:"display_name"`
		Description           string   `json:"description"`
		SupportedEffortLevels []string `json:"supported_effort_levels"`
		ID                    string   `json:"-"`
	} `json:"models"`
}

type grokResult struct {
	Version   string
	FetchedAt time.Time
	Models    []sidecarModel
}

type commandRunner func(context.Context, string, ...string) ([]byte, error)

func init() {
	logging.SetupBaseLogger()
}

func main() {
	var provider string
	var authID string
	var outputPath string
	var configPath string
	var claudeCacheDir string
	var grokBinary string
	var grokHome string
	flag.StringVar(&provider, "provider", "", "Provider to sync: claude or xai")
	flag.StringVar(&authID, "auth-id", "", "CLIProxyAPI auth ID to update")
	flag.StringVar(&outputPath, "output", "oauth-model-availability.json", "Sidecar JSON path")
	flag.StringVar(&configPath, "config", "config.yaml", "CLIProxyAPI config path")
	flag.StringVar(&claudeCacheDir, "claude-cache-dir", "", "Claude SDK helper cache directory")
	flag.StringVar(&grokBinary, "grok-binary", "", "Official grok executable override (empty installs latest into the user cache)")
	flag.StringVar(&grokHome, "grok-home", "", "Official grok state directory")
	flag.Parse()

	started := time.Now()
	provider = strings.ToLower(strings.TrimSpace(provider))
	authID = strings.TrimSpace(authID)
	if provider != "claude" && provider != "xai" {
		log.Error("sync failed: provider must be claude or xai")
		os.Exit(1)
	}
	if authID == "" {
		log.Error("sync failed: auth-id is required")
		os.Exit(1)
	}

	entry, errSync := syncCredential(context.Background(), provider, authID, configPath, claudeCacheDir, grokBinary, grokHome, runCommand)
	if errSync != nil {
		log.WithFields(log.Fields{"provider": provider, "auth_id": authID, "reason": safeReason(errSync)}).Error("OAuth model availability sync failed")
		os.Exit(1)
	}
	added, removed, errUpdate := updateSidecar(outputPath, entry)
	if errUpdate != nil {
		log.WithFields(log.Fields{"provider": provider, "auth_id": authID, "reason": safeReason(errUpdate)}).Error("OAuth model availability sidecar update failed")
		os.Exit(1)
	}
	log.WithFields(log.Fields{
		"provider":       provider,
		"auth_id":        authID,
		"client_version": entry.Client.Version,
		"models":         len(entry.Models),
		"duration":       time.Since(started).Round(time.Millisecond),
	}).Info("OAuth model availability sidecar updated")
	log.WithFields(log.Fields{"provider": provider, "auth_id": authID, "added": added, "removed": removed}).Debug("OAuth model availability model changes")
}

func syncCredential(ctx context.Context, provider, authID, configPath, claudeCacheDir, grokBinary, grokHome string, runner commandRunner) (sidecarCredential, error) {
	auth, errAuth := loadAuth(ctx, configPath, authID, provider)
	if errAuth != nil {
		return sidecarCredential{}, errAuth
	}
	expectedEmail := metadataString(auth.Metadata, "email")
	expectedSubject := metadataString(auth.Metadata, "sub")

	switch provider {
	case "claude":
		result, errClaude := collectClaude(ctx, claudeCacheDir, runner)
		if errClaude != nil {
			return sidecarCredential{}, errClaude
		}
		if result.Account.APIProvider != "firstParty" {
			return sidecarCredential{}, fmt.Errorf("official client is not using Anthropic first-party OAuth")
		}
		if errIdentity := verifyIdentity(expectedEmail, result.Account.Email, expectedSubject, ""); errIdentity != nil {
			return sidecarCredential{}, errIdentity
		}
		models := make([]sidecarModel, 0, len(result.Models))
		for _, model := range result.Models {
			models = append(models, sidecarModel{
				ID:                    model.ID,
				DisplayName:           model.DisplayName,
				Description:           model.Description,
				SupportedEffortLevels: append([]string(nil), model.SupportedEffortLevels...),
			})
		}
		return newCredential(provider, authID, result.Client, models)
	case "xai":
		result, observedEmail, observedSubject, client, errGrok := collectGrok(ctx, grokBinary, grokHome, runner)
		if errGrok != nil {
			return sidecarCredential{}, errGrok
		}
		if errIdentity := verifyIdentity(expectedEmail, observedEmail, expectedSubject, observedSubject); errIdentity != nil {
			return sidecarCredential{}, errIdentity
		}
		if client.Version == "" {
			client.Version = result.Version
		}
		return newCredential(provider, authID, client, result.Models)
	default:
		return sidecarCredential{}, fmt.Errorf("unsupported provider")
	}
}

func newCredential(provider, authID string, client sidecarClient, models []sidecarModel) (sidecarCredential, error) {
	models = normalizeModels(models)
	if len(models) == 0 {
		return sidecarCredential{}, fmt.Errorf("official client returned no models")
	}
	return sidecarCredential{Provider: provider, AuthID: authID, Client: client, Models: models}, nil
}

func collectClaude(ctx context.Context, cacheDir string, runner commandRunner) (claudeResult, error) {
	if strings.TrimSpace(cacheDir) == "" {
		userCache, errCache := os.UserCacheDir()
		if errCache != nil {
			return claudeResult{}, fmt.Errorf("resolve user cache directory: %w", errCache)
		}
		cacheDir = filepath.Join(userCache, "cli-proxy-api", "oauth-model-availability", "claude")
	}
	if errMkdir := os.MkdirAll(cacheDir, 0o700); errMkdir != nil {
		return claudeResult{}, fmt.Errorf("prepare Claude helper cache: %w", errMkdir)
	}
	adapterPath := filepath.Join(cacheDir, "sync.mjs")
	if errWrite := os.WriteFile(adapterPath, []byte(claudeAdapter), 0o600); errWrite != nil {
		return claudeResult{}, fmt.Errorf("prepare Claude helper adapter: %w", errWrite)
	}
	if _, errInstall := runner(ctx, "pnpm", "--dir", cacheDir, "add", "--save-exact", claudePackage+"@latest"); errInstall != nil {
		return claudeResult{}, fmt.Errorf("install official Claude Agent SDK")
	}
	raw, errRun := runner(ctx, "node", adapterPath)
	if errRun != nil {
		return claudeResult{}, fmt.Errorf("run official Claude Agent SDK helper")
	}
	return parseClaudeResult(raw)
}

func parseClaudeResult(raw []byte) (claudeResult, error) {
	var result claudeResult
	if errUnmarshal := json.Unmarshal(raw, &result); errUnmarshal != nil {
		return claudeResult{}, fmt.Errorf("parse Claude SDK result: %w", errUnmarshal)
	}
	seen := make(map[string]struct{}, len(result.Models))
	filtered := result.Models[:0]
	for _, model := range result.Models {
		model.ID = strings.TrimSpace(model.ResolvedModel)
		if model.ID == "" {
			model.ID = strings.TrimSpace(model.Value)
		}
		key := strings.ToLower(model.ID)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, model)
	}
	result.Models = filtered
	if result.Client.Name != claudePackage || result.Client.Version == "" || len(result.Models) == 0 {
		return claudeResult{}, fmt.Errorf("Claude SDK returned incomplete initialization data")
	}
	return result, nil
}

func collectGrok(ctx context.Context, binary, grokHome string, runner commandRunner) (grokResult, string, string, sidecarClient, error) {
	if strings.TrimSpace(binary) == "" {
		userCache, errCache := os.UserCacheDir()
		if errCache != nil {
			return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("resolve user cache directory: %w", errCache)
		}
		installRoot := filepath.Join(userCache, "cli-proxy-api", "oauth-model-availability", "grok")
		if errMkdir := os.MkdirAll(installRoot, 0o700); errMkdir != nil {
			return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("prepare grok helper cache: %w", errMkdir)
		}
		if _, errInstall := runner(ctx, "cargo", "install", "--git", "https://github.com/xai-org/grok-build", "--root", installRoot, "--locked", "--force", "xai-grok-pager-bin"); errInstall != nil {
			return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("install official grok client")
		}
		binary = filepath.Join(installRoot, "bin", "xai-grok-pager")
		if os.PathSeparator == '\\' {
			binary += ".exe"
		}
	}
	if strings.TrimSpace(grokHome) == "" {
		userHome, errHome := os.UserHomeDir()
		if errHome != nil {
			return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("resolve user home: %w", errHome)
		}
		grokHome = filepath.Join(userHome, ".grok")
	}
	if _, errModels := runner(ctx, binary, "models"); errModels != nil {
		return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("run official grok models command")
	}
	cacheRaw, errCache := os.ReadFile(filepath.Join(grokHome, "models_cache.json"))
	if errCache != nil {
		return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("read official grok model cache: %w", errCache)
	}
	result, errParse := parseGrokCache(cacheRaw)
	if errParse != nil {
		return grokResult{}, "", "", sidecarClient{}, errParse
	}
	if age := time.Since(result.FetchedAt); age < 0 || age > 10*time.Minute {
		return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("official grok model cache is stale")
	}
	authRaw, errAuth := os.ReadFile(filepath.Join(grokHome, "auth.json"))
	if errAuth != nil {
		return grokResult{}, "", "", sidecarClient{}, fmt.Errorf("read official grok auth state: %w", errAuth)
	}
	observedEmail, observedSubject := extractGrokIdentity(authRaw)
	versionRaw, _ := runner(ctx, binary, "--version")
	client := sidecarClient{Name: "xai-org/grok-build", Version: firstVersion(string(versionRaw))}
	if binaryPath, errLook := exec.LookPath(binary); errLook == nil {
		client.ArtifactSHA256, _ = hashFile(binaryPath)
	}
	return result, observedEmail, observedSubject, client, nil
}

func parseGrokCache(raw []byte) (grokResult, error) {
	var cache struct {
		FetchedAt  time.Time `json:"fetched_at"`
		Version    string    `json:"grok_version"`
		AuthMethod string    `json:"auth_method"`
		Models     map[string]struct {
			Info struct {
				Model               string `json:"model"`
				Name                string `json:"name"`
				Description         string `json:"description"`
				ContextWindow       int    `json:"context_window"`
				MaxCompletionTokens int    `json:"max_completion_tokens"`
				Hidden              bool   `json:"hidden"`
			} `json:"info"`
		} `json:"models"`
	}
	if errUnmarshal := json.Unmarshal(raw, &cache); errUnmarshal != nil {
		return grokResult{}, fmt.Errorf("parse official grok model cache: %w", errUnmarshal)
	}
	if cache.AuthMethod != "session" {
		return grokResult{}, fmt.Errorf("official grok model cache was not fetched with OAuth session auth")
	}
	models := make([]sidecarModel, 0, len(cache.Models))
	for _, entry := range cache.Models {
		if entry.Info.Hidden {
			continue
		}
		models = append(models, sidecarModel{
			ID:                  entry.Info.Model,
			DisplayName:         entry.Info.Name,
			Description:         entry.Info.Description,
			ContextLength:       entry.Info.ContextWindow,
			MaxCompletionTokens: entry.Info.MaxCompletionTokens,
		})
	}
	models = normalizeModels(models)
	if cache.FetchedAt.IsZero() || cache.Version == "" || len(models) == 0 {
		return grokResult{}, fmt.Errorf("official grok model cache is incomplete")
	}
	return grokResult{Version: cache.Version, FetchedAt: cache.FetchedAt, Models: models}, nil
}

func loadAuth(ctx context.Context, configPath, authID, provider string) (*coreauth.Auth, error) {
	cfg, errConfig := config.LoadConfigOptional(configPath, false)
	if errConfig != nil {
		return nil, fmt.Errorf("load CLIProxyAPI config: %w", errConfig)
	}
	if cfg == nil {
		return nil, fmt.Errorf("load CLIProxyAPI config: configuration not found")
	}
	authDir, errResolve := util.ResolveAuthDir(cfg.AuthDir)
	if errResolve != nil {
		return nil, fmt.Errorf("resolve auth directory: %w", errResolve)
	}
	store := sdkauth.NewFileTokenStore()
	store.SetBaseDir(authDir)
	auths, errList := store.List(ctx)
	if errList != nil {
		return nil, fmt.Errorf("list CLIProxyAPI credentials: %w", errList)
	}
	for _, auth := range auths {
		if auth != nil && auth.ID == authID && strings.EqualFold(strings.TrimSpace(auth.Provider), provider) && auth.AuthKind() == coreauth.AuthKindOAuth {
			return auth, nil
		}
	}
	return nil, fmt.Errorf("matching OAuth credential not found")
}

func verifyIdentity(expectedEmail, observedEmail, expectedSubject, observedSubject string) error {
	expectedEmail = strings.TrimSpace(expectedEmail)
	observedEmail = strings.TrimSpace(observedEmail)
	if expectedEmail != "" && observedEmail != "" {
		if strings.EqualFold(expectedEmail, observedEmail) {
			return nil
		}
		return fmt.Errorf("official client account does not match requested credential")
	}
	expectedSubject = strings.TrimSpace(expectedSubject)
	observedSubject = strings.TrimSpace(observedSubject)
	if expectedSubject != "" && observedSubject != "" {
		if expectedSubject == observedSubject {
			return nil
		}
		return fmt.Errorf("official client account does not match requested credential")
	}
	return fmt.Errorf("official client account identity could not be verified")
}

func updateSidecar(path string, entry sidecarCredential) ([]string, []string, error) {
	path = filepath.Clean(path)
	document := sidecarDocument{SchemaVersion: 1, Credentials: []sidecarCredential{}}
	if raw, errRead := os.ReadFile(path); errRead == nil {
		if errUnmarshal := json.Unmarshal(raw, &document); errUnmarshal != nil {
			return nil, nil, fmt.Errorf("parse existing sidecar: %w", errUnmarshal)
		}
		if document.SchemaVersion != 1 {
			return nil, nil, fmt.Errorf("existing sidecar has unsupported schema version")
		}
	} else if !errors.Is(errRead, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read existing sidecar: %w", errRead)
	}

	oldModels := []sidecarModel(nil)
	replaced := false
	for i := range document.Credentials {
		if strings.EqualFold(document.Credentials[i].Provider, entry.Provider) && document.Credentials[i].AuthID == entry.AuthID {
			oldModels = document.Credentials[i].Models
			document.Credentials[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		document.Credentials = append(document.Credentials, entry)
	}
	sort.Slice(document.Credentials, func(i, j int) bool {
		left := strings.ToLower(document.Credentials[i].Provider) + "\x00" + document.Credentials[i].AuthID
		right := strings.ToLower(document.Credentials[j].Provider) + "\x00" + document.Credentials[j].AuthID
		return left < right
	})
	document.SchemaVersion = 1
	document.GeneratedAt = time.Now().UTC()
	raw, errMarshal := json.MarshalIndent(document, "", "  ")
	if errMarshal != nil {
		return nil, nil, fmt.Errorf("encode sidecar: %w", errMarshal)
	}
	raw = append(raw, '\n')
	if errMkdir := os.MkdirAll(filepath.Dir(path), 0o755); errMkdir != nil {
		return nil, nil, fmt.Errorf("prepare sidecar directory: %w", errMkdir)
	}
	temp, errCreate := os.CreateTemp(filepath.Dir(path), ".oauth-model-availability-*.tmp")
	if errCreate != nil {
		return nil, nil, fmt.Errorf("create temporary sidecar: %w", errCreate)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if errChmod := temp.Chmod(0o600); errChmod != nil {
		_ = temp.Close()
		return nil, nil, fmt.Errorf("secure temporary sidecar: %w", errChmod)
	}
	if _, errWrite := temp.Write(raw); errWrite != nil {
		_ = temp.Close()
		return nil, nil, fmt.Errorf("write temporary sidecar: %w", errWrite)
	}
	if errSync := temp.Sync(); errSync != nil {
		_ = temp.Close()
		return nil, nil, fmt.Errorf("sync temporary sidecar: %w", errSync)
	}
	if errClose := temp.Close(); errClose != nil {
		return nil, nil, fmt.Errorf("close temporary sidecar: %w", errClose)
	}
	if errRename := replaceFile(tempPath, path); errRename != nil {
		return nil, nil, fmt.Errorf("replace sidecar: %w", errRename)
	}
	return modelDiff(oldModels, entry.Models)
}

func modelDiff(oldModels, newModels []sidecarModel) ([]string, []string, error) {
	oldSet := make(map[string]string, len(oldModels))
	newSet := make(map[string]string, len(newModels))
	for _, model := range oldModels {
		oldSet[strings.ToLower(model.ID)] = model.ID
	}
	for _, model := range newModels {
		newSet[strings.ToLower(model.ID)] = model.ID
	}
	added := make([]string, 0)
	removed := make([]string, 0)
	for key, id := range newSet {
		if _, exists := oldSet[key]; !exists {
			added = append(added, id)
		}
	}
	for key, id := range oldSet {
		if _, exists := newSet[key]; !exists {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed, nil
}

func normalizeModels(models []sidecarModel) []sidecarModel {
	seen := make(map[string]struct{}, len(models))
	result := make([]sidecarModel, 0, len(models))
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		key := strings.ToLower(model.ID)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].ID) < strings.ToLower(result[j].ID) })
	return result
}

func extractGrokIdentity(raw []byte) (string, string) {
	var store map[string]struct {
		AuthMode string `json:"auth_mode"`
		Email    string `json:"email"`
		UserID   string `json:"user_id"`
	}
	if json.Unmarshal(raw, &store) != nil {
		return "", ""
	}
	type identity struct {
		email   string
		subject string
	}
	identities := make(map[identity]struct{})
	for _, auth := range store {
		switch strings.ToLower(strings.TrimSpace(auth.AuthMode)) {
		case "oidc", "external":
			current := identity{email: strings.TrimSpace(auth.Email), subject: strings.TrimSpace(auth.UserID)}
			if current.email != "" || current.subject != "" {
				identities[current] = struct{}{}
			}
		}
	}
	if len(identities) != 1 {
		return "", ""
	}
	for current := range identities {
		return current.email, current.subject
	}
	return "", ""
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	return stringValue(metadata[key])
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	return command.Output()
}

func hashFile(path string) (string, error) {
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		return "", errRead
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func firstVersion(value string) string {
	for _, field := range strings.Fields(value) {
		if strings.ContainsAny(field, "0123456789") {
			return strings.TrimPrefix(field, "v")
		}
	}
	return ""
}

func safeReason(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

const claudeAdapter = `import { query } from "@anthropic-ai/claude-agent-sdk";
import { createHash } from "node:crypto";
import { readdir, readFile, realpath } from "node:fs/promises";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

async function hashDirectory(root) {
  const hash = createHash("sha256");
  async function visit(directory) {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of entries) {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) {
        await visit(path);
      } else if (entry.isFile()) {
        hash.update(relative(root, path));
        hash.update("\0");
        hash.update(await readFile(path));
      }
    }
  }
  await visit(root);
  return hash.digest("hex");
}

const packageURL = new URL("./node_modules/@anthropic-ai/claude-agent-sdk/package.json", import.meta.url);
const packageRaw = await readFile(packageURL);
const packageJSON = JSON.parse(packageRaw.toString("utf8"));
const packageRoot = await realpath(dirname(fileURLToPath(packageURL)));
const session = query({
  prompt: (async function* () {})(),
  options: { tools: [], persistSession: false, maxTurns: 0 }
});
try {
  const initialization = await session.initializationResult();
  const models = await session.supportedModels();
  process.stdout.write(JSON.stringify({
    client: {
      name: "@anthropic-ai/claude-agent-sdk",
      version: packageJSON.version,
      artifact_sha256: await hashDirectory(packageRoot)
    },
    account: {
      email: initialization.account?.email,
      api_provider: initialization.account?.apiProvider
    },
    models: models.map(model => ({
      value: model.value,
      resolved_model: model.resolvedModel,
      display_name: model.displayName,
      description: model.description,
      supported_effort_levels: model.supportedEffortLevels
    }))
  }));
} finally {
  session.close();
}
`
