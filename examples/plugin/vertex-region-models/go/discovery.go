package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type authModelRPCRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type discoveryLogger func(callbackID, level, message string, fields map[string]any)

type discoveryService struct {
	cache *matrixCache
	log   discoveryLogger
}

func (s *discoveryService) modelsForAuth(req authModelRPCRequest, cfg pluginConfig) (pluginapi.ModelResponse, error) {
	candidates := append([]pluginapi.ModelInfo(nil), req.CandidateModels...)
	location := credentialLocation(req.AuthModelRequest)
	fallback := func() pluginapi.ModelResponse {
		models := []pluginapi.ModelInfo(nil)
		if cfg.FailOpen {
			models = candidates
		}
		return pluginapi.ModelResponse{Provider: "vertex", Models: models}
	}
	if s == nil || s.cache == nil {
		s.writeLog(req.HostCallbackID, "warn", "vertex region model catalog unavailable", map[string]any{
			"auth_id":         req.AuthID,
			"location":        location,
			"candidate_count": len(candidates),
			"fail_open":       cfg.FailOpen,
			"error":           "location matrix cache is unavailable",
		})
		return fallback(), nil
	}
	matrix, errMatrix := s.cache.get(req.HostCallbackID, cfg)
	if errMatrix != nil {
		s.writeLog(req.HostCallbackID, "warn", "vertex region model catalog unavailable", map[string]any{
			"auth_id":         req.AuthID,
			"location":        location,
			"candidate_count": len(candidates),
			"fail_open":       cfg.FailOpen,
			"error":           errMatrix.Error(),
		})
		return fallback(), nil
	}
	documented, okLocation := matrix[location]
	if !okLocation {
		s.writeLog(req.HostCallbackID, "warn", "vertex credential location is absent from the model catalog", map[string]any{
			"auth_id":         req.AuthID,
			"location":        location,
			"candidate_count": len(candidates),
			"fail_open":       cfg.FailOpen,
		})
		return fallback(), nil
	}
	candidatesByID := make(map[string]pluginapi.ModelInfo, len(candidates))
	for _, candidate := range candidates {
		modelID := strings.ToLower(strings.TrimSpace(candidate.ID))
		if modelID != "" {
			candidatesByID[modelID] = candidate
		}
	}
	models := make([]pluginapi.ModelInfo, 0, len(documented))
	for _, model := range documented {
		candidate, exact := candidatesByID[model.ID]
		if !exact {
			candidate, _ = candidateMetadataDonor(candidatesByID, model.ID)
		}
		models = append(models, authoritativeModelInfo(model, candidate, exact))
	}
	s.writeLog(req.HostCallbackID, "info", "vertex region model catalog resolved", map[string]any{
		"auth_id":         req.AuthID,
		"location":        location,
		"candidate_count": len(candidates),
		"model_count":     len(models),
	})
	return pluginapi.ModelResponse{Provider: "vertex", Models: models}, nil
}

func (s *discoveryService) writeLog(callbackID, level, message string, fields map[string]any) {
	if s != nil && s.log != nil {
		s.log(callbackID, level, message, fields)
	}
}

func candidateMetadataDonor(candidates map[string]pluginapi.ModelInfo, modelID string) (pluginapi.ModelInfo, bool) {
	if strings.HasSuffix(modelID, "-preview") {
		if candidate, okCandidate := candidates[strings.TrimSuffix(modelID, "-preview")]; okCandidate {
			return candidate, true
		}
	}
	return pluginapi.ModelInfo{}, false
}

func authoritativeModelInfo(documented documentedModel, candidate pluginapi.ModelInfo, exact bool) pluginapi.ModelInfo {
	model := candidate
	model.ID = documented.ID
	if model.Object == "" {
		model.Object = "model"
	}
	if model.OwnedBy == "" {
		model.OwnedBy = "google"
	}
	if model.Type == "" {
		model.Type = "gemini"
	}
	if !exact || strings.TrimSpace(model.Name) == "" {
		model.Name = "models/" + documented.ID
	}
	if !exact || strings.TrimSpace(model.DisplayName) == "" {
		model.DisplayName = documented.DisplayName
	}
	return model
}
