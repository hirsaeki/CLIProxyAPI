package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

const localModelsOverlayEnv = "CLIPROXY_LOCAL_MODELS_JSON"

func applyLocalModelsOverlay(data *staticModelsJSON, source string) {
	if data == nil {
		return
	}

	path := strings.TrimSpace(os.Getenv(localModelsOverlayEnv))
	if path == "" {
		return
	}

	overlay, err := loadLocalModelsOverlay(path)
	if err != nil {
		log.Warnf("registry: failed to load local models overlay from %s=%q: %v", localModelsOverlayEnv, path, err)
		return
	}

	merged := cloneStaticModelsCatalog(data)
	mergeLocalModelsOverlay(merged, overlay)
	if err := validateModelsCatalog(merged); err != nil {
		log.Warnf("registry: local models overlay from %s=%q produced invalid catalog for %s, ignoring overlay: %v", localModelsOverlayEnv, path, source, err)
		return
	}

	*data = *merged
	log.Infof("registry: applied local models overlay from %s=%q for %s", localModelsOverlayEnv, path, source)
}

func loadLocalModelsOverlay(path string) (*staticModelsJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read overlay: %w", err)
	}

	var overlay staticModelsJSON
	if err := json.Unmarshal(data, &overlay); err != nil {
		return nil, fmt.Errorf("decode overlay: %w", err)
	}
	if err := validateLocalModelsOverlay(&overlay); err != nil {
		return nil, err
	}
	return &overlay, nil
}

func validateLocalModelsOverlay(overlay *staticModelsJSON) error {
	if overlay == nil {
		return fmt.Errorf("overlay is nil")
	}
	for _, section := range []struct {
		name   string
		models []*ModelInfo
	}{
		{name: "gemini", models: overlay.Gemini},
		{name: "vertex", models: overlay.Vertex},
		{name: "aistudio", models: overlay.AIStudio},
	} {
		if len(section.models) == 0 {
			continue
		}
		if err := validateModelSection(section.name, section.models); err != nil {
			return fmt.Errorf("validate overlay %s: %w", section.name, err)
		}
	}
	return nil
}

func mergeLocalModelsOverlay(base, overlay *staticModelsJSON) {
	if base == nil || overlay == nil {
		return
	}
	base.Gemini = upsertModelInfos(base.Gemini, overlay.Gemini...)
	base.Vertex = upsertModelInfos(base.Vertex, overlay.Vertex...)
	base.AIStudio = upsertModelInfos(base.AIStudio, overlay.AIStudio...)
}

func cloneStaticModelsCatalog(data *staticModelsJSON) *staticModelsJSON {
	if data == nil {
		return nil
	}
	return &staticModelsJSON{
		Claude:      cloneModelInfos(data.Claude),
		Gemini:      cloneModelInfos(data.Gemini),
		Vertex:      cloneModelInfos(data.Vertex),
		AIStudio:    cloneModelInfos(data.AIStudio),
		CodexFree:   cloneModelInfos(data.CodexFree),
		CodexTeam:   cloneModelInfos(data.CodexTeam),
		CodexPlus:   cloneModelInfos(data.CodexPlus),
		CodexPro:    cloneModelInfos(data.CodexPro),
		Kimi:        cloneModelInfos(data.Kimi),
		Antigravity: cloneModelInfos(data.Antigravity),
		XAI:         cloneModelInfos(data.XAI),
	}
}
