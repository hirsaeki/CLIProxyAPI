package cliproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	log "github.com/sirupsen/logrus"
)

const oauthModelAvailabilitySchemaVersion = 1

type oauthModelAvailabilityDocument struct {
	SchemaVersion int                                `json:"schema_version"`
	GeneratedAt   time.Time                          `json:"generated_at"`
	Credentials   []oauthModelAvailabilityCredential `json:"credentials"`
}

type oauthModelAvailabilityCredential struct {
	Provider string                        `json:"provider"`
	AuthID   string                        `json:"auth_id"`
	Client   oauthModelAvailabilityClient  `json:"client"`
	Models   []oauthModelAvailabilityModel `json:"models"`
}

type oauthModelAvailabilityClient struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
}

type oauthModelAvailabilityModel struct {
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

type oauthModelAvailabilityKey struct {
	provider string
	authID   string
}

type oauthModelAvailabilitySnapshot struct {
	generatedAt time.Time
	entries     map[oauthModelAvailabilityKey]oauthModelAvailabilityCredential
}

func loadOAuthModelAvailability(configuredPath, configPath string) (*oauthModelAvailabilitySnapshot, string, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return nil, "", nil
	}
	resolvedPath := configuredPath
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(filepath.Dir(configPath), resolvedPath)
	}
	resolvedPath = filepath.Clean(resolvedPath)
	raw, errRead := os.ReadFile(resolvedPath)
	if errRead != nil {
		return nil, resolvedPath, fmt.Errorf("read OAuth model availability file: %w", errRead)
	}
	var document oauthModelAvailabilityDocument
	if errUnmarshal := json.Unmarshal(raw, &document); errUnmarshal != nil {
		return nil, resolvedPath, fmt.Errorf("parse OAuth model availability file: %w", errUnmarshal)
	}
	if errValidate := validateOAuthModelAvailability(&document); errValidate != nil {
		return nil, resolvedPath, errValidate
	}
	snapshot := &oauthModelAvailabilitySnapshot{
		generatedAt: document.GeneratedAt,
		entries:     make(map[oauthModelAvailabilityKey]oauthModelAvailabilityCredential, len(document.Credentials)),
	}
	modelCount := 0
	for _, entry := range document.Credentials {
		entry.Provider = strings.ToLower(strings.TrimSpace(entry.Provider))
		entry.AuthID = strings.TrimSpace(entry.AuthID)
		for i := range entry.Models {
			entry.Models[i].ID = strings.TrimSpace(entry.Models[i].ID)
		}
		snapshot.entries[oauthModelAvailabilityKey{provider: entry.Provider, authID: entry.AuthID}] = entry
		modelCount += len(entry.Models)
	}
	log.WithFields(log.Fields{
		"credentials": len(snapshot.entries),
		"models":      modelCount,
		"path":        resolvedPath,
	}).Info("loaded OAuth model availability sidecar")
	if age := time.Since(snapshot.generatedAt); age > 24*time.Hour {
		log.WithFields(log.Fields{"age": age.Round(time.Minute), "path": resolvedPath}).Warn("OAuth model availability sidecar is stale")
	}
	return snapshot, resolvedPath, nil
}

func validateOAuthModelAvailability(document *oauthModelAvailabilityDocument) error {
	if document == nil {
		return fmt.Errorf("validate OAuth model availability file: empty document")
	}
	if document.SchemaVersion != oauthModelAvailabilitySchemaVersion {
		return fmt.Errorf("validate OAuth model availability file: unsupported schema_version %d", document.SchemaVersion)
	}
	if document.GeneratedAt.IsZero() {
		return fmt.Errorf("validate OAuth model availability file: generated_at is required")
	}
	seenCredentials := make(map[oauthModelAvailabilityKey]struct{}, len(document.Credentials))
	for credentialIndex := range document.Credentials {
		entry := &document.Credentials[credentialIndex]
		provider := strings.ToLower(strings.TrimSpace(entry.Provider))
		if provider != "claude" && provider != "xai" {
			return fmt.Errorf("validate OAuth model availability file: credential %d has unsupported provider %q", credentialIndex, entry.Provider)
		}
		authID := strings.TrimSpace(entry.AuthID)
		if authID == "" {
			return fmt.Errorf("validate OAuth model availability file: credential %d has empty auth_id", credentialIndex)
		}
		key := oauthModelAvailabilityKey{provider: provider, authID: authID}
		if _, exists := seenCredentials[key]; exists {
			return fmt.Errorf("validate OAuth model availability file: duplicate credential for provider %q and auth_id %q", provider, authID)
		}
		seenCredentials[key] = struct{}{}
		if len(entry.Models) == 0 {
			return fmt.Errorf("validate OAuth model availability file: credential %q has no models", authID)
		}
		seenModels := make(map[string]struct{}, len(entry.Models))
		for modelIndex := range entry.Models {
			modelID := strings.TrimSpace(entry.Models[modelIndex].ID)
			if modelID == "" {
				return fmt.Errorf("validate OAuth model availability file: credential %q model %d has empty id", authID, modelIndex)
			}
			modelKey := strings.ToLower(modelID)
			if _, exists := seenModels[modelKey]; exists {
				return fmt.Errorf("validate OAuth model availability file: credential %q has duplicate model %q", authID, modelID)
			}
			seenModels[modelKey] = struct{}{}
		}
	}
	return nil
}

func (s *oauthModelAvailabilitySnapshot) lookup(provider, authID string) (oauthModelAvailabilityCredential, bool) {
	if s == nil {
		return oauthModelAvailabilityCredential{}, false
	}
	entry, ok := s.entries[oauthModelAvailabilityKey{
		provider: strings.ToLower(strings.TrimSpace(provider)),
		authID:   strings.TrimSpace(authID),
	}]
	return entry, ok
}

func (s *Service) applyOAuthModelAvailability(provider, authID, authKind string, native []*ModelInfo) []*ModelInfo {
	if s == nil || s.oauthModelAvailability == nil || !strings.EqualFold(strings.TrimSpace(authKind), "oauth") {
		return native
	}
	entry, ok := s.oauthModelAvailability.lookup(provider, authID)
	if !ok {
		log.WithFields(log.Fields{"provider": provider, "auth_id": authID}).Warn("OAuth model availability entry not found; using central catalog")
		return native
	}
	nativeByID := make(map[string]*ModelInfo, len(native))
	for _, model := range native {
		if model != nil {
			nativeByID[strings.ToLower(strings.TrimSpace(model.ID))] = model
		}
	}
	result := make([]*ModelInfo, 0, len(entry.Models))
	added := make([]string, 0)
	allowed := make(map[string]struct{}, len(entry.Models))
	for _, available := range entry.Models {
		key := strings.ToLower(strings.TrimSpace(available.ID))
		allowed[key] = struct{}{}
		if existing := nativeByID[key]; existing != nil {
			result = append(result, existing)
			continue
		}
		result = append(result, sidecarModelInfo(provider, available))
		added = append(added, available.ID)
	}
	removed := make([]string, 0)
	for _, model := range native {
		if model == nil {
			continue
		}
		if _, exists := allowed[strings.ToLower(strings.TrimSpace(model.ID))]; !exists {
			removed = append(removed, model.ID)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	log.WithFields(log.Fields{
		"provider": provider,
		"auth_id":  authID,
		"models":   len(result),
	}).Info("applied OAuth model availability sidecar")
	log.WithFields(log.Fields{
		"provider": provider,
		"auth_id":  authID,
		"added":    added,
		"removed":  removed,
	}).Debug("OAuth model availability differs from central catalog")
	return result
}

func (s *Service) hasAuthoritativeOAuthModelAvailability(provider, authID, authKind string) bool {
	if s == nil || s.oauthModelAvailability == nil || !strings.EqualFold(strings.TrimSpace(authKind), "oauth") {
		return false
	}
	_, ok := s.oauthModelAvailability.lookup(provider, authID)
	return ok
}

func sidecarModelInfo(provider string, model oauthModelAvailabilityModel) *registry.ModelInfo {
	provider = strings.ToLower(strings.TrimSpace(provider))
	ownedBy := provider
	if provider == "claude" {
		ownedBy = "anthropic"
	}
	info := &registry.ModelInfo{
		ID:                        strings.TrimSpace(model.ID),
		Object:                    "model",
		OwnedBy:                   ownedBy,
		Type:                      provider,
		DisplayName:               strings.TrimSpace(model.DisplayName),
		Description:               strings.TrimSpace(model.Description),
		ContextLength:             model.ContextLength,
		MaxCompletionTokens:       model.MaxCompletionTokens,
		SupportedParameters:       append([]string(nil), model.SupportedParameters...),
		SupportedInputModalities:  append([]string(nil), model.SupportedInputModalities...),
		SupportedOutputModalities: append([]string(nil), model.SupportedOutputModalities...),
	}
	if len(model.SupportedEffortLevels) > 0 {
		info.Thinking = &registry.ThinkingSupport{Levels: append([]string(nil), model.SupportedEffortLevels...)}
	}
	return info
}
