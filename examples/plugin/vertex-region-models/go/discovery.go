package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type authModelRPCRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type discoveryService struct {
	cache *matrixCache
}

func (s *discoveryService) modelsForAuth(req authModelRPCRequest, cfg pluginConfig) (pluginapi.ModelResponse, error) {
	candidates := append([]pluginapi.ModelInfo(nil), req.CandidateModels...)
	fallback := func() pluginapi.ModelResponse {
		models := []pluginapi.ModelInfo(nil)
		if cfg.FailOpen {
			models = candidates
		}
		return pluginapi.ModelResponse{Provider: "vertex", Models: models}
	}
	if s == nil || s.cache == nil {
		return fallback(), nil
	}
	matrix, errMatrix := s.cache.get(req.HostCallbackID, cfg)
	if errMatrix != nil {
		return fallback(), nil
	}
	location := credentialLocation(req.AuthModelRequest)
	supported, okLocation := matrix[location]
	if !okLocation {
		return fallback(), nil
	}
	filtered := make([]pluginapi.ModelInfo, 0, len(candidates))
	for _, candidate := range candidates {
		modelID := strings.ToLower(strings.TrimSpace(candidate.ID))
		if _, okSupported := supported[modelID]; okSupported {
			filtered = append(filtered, candidate)
		}
	}
	return pluginapi.ModelResponse{Provider: "vertex", Models: filtered}, nil
}
