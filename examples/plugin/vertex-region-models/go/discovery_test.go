package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestDiscoveryFiltersCandidatesAndPreservesMetadata(t *testing.T) {
	service := &discoveryService{cache: newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(matrixFixture)}, nil
	})}
	request := authModelRPCRequest{
		AuthModelRequest: pluginapi.AuthModelRequest{
			AuthID:       "vertex-auth",
			AuthProvider: "vertex",
			Metadata:     map[string]any{"location": "us"},
			CandidateModels: []pluginapi.ModelInfo{
				{ID: "gemini-multi", DisplayName: "Multi", SupportedParameters: []string{"temperature"}, Thinking: &pluginapi.ThinkingSupport{Min: 128, Max: 32768}},
				{ID: "gemini-unsupported", DisplayName: "Unsupported"},
			},
		},
		HostCallbackID: "callback",
	}

	response, errModels := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: true})
	if errModels != nil {
		t.Fatalf("modelsForAuth() error = %v", errModels)
	}
	if response.Provider != "vertex" || len(response.Models) != 1 {
		t.Fatalf("response = %#v", response)
	}
	model := response.Models[0]
	if model.ID != "gemini-multi" || model.DisplayName != "Multi" || model.Thinking == nil || model.Thinking.Max != 32768 {
		t.Fatalf("filtered model metadata = %#v", model)
	}
}

func TestDiscoveryFailurePolicy(t *testing.T) {
	service := &discoveryService{cache: newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{}, errors.New("network down")
	})}
	request := authModelRPCRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		Metadata:        map[string]any{"location": "us"},
		CandidateModels: []pluginapi.ModelInfo{{ID: "gemini-candidate"}},
	}}

	openResponse, errOpen := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: true})
	if errOpen != nil || len(openResponse.Models) != 1 {
		t.Fatalf("fail-open response = %#v, %v", openResponse, errOpen)
	}
	closedResponse, errClosed := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: false})
	if errClosed != nil || len(closedResponse.Models) != 0 {
		t.Fatalf("fail-closed response = %#v, %v", closedResponse, errClosed)
	}
}

func TestDiscoveryUnknownLocationUsesFailurePolicy(t *testing.T) {
	service := &discoveryService{cache: newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(matrixFixture)}, nil
	})}
	request := authModelRPCRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		Metadata:        map[string]any{"location": "moon-1"},
		CandidateModels: []pluginapi.ModelInfo{{ID: "gemini-candidate"}},
	}}

	response, errModels := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: true})
	if errModels != nil || len(response.Models) != 1 {
		t.Fatalf("unknown-location fail-open response = %#v, %v", response, errModels)
	}
}

func TestPluginRegistrationBindsVertexModelProvider(t *testing.T) {
	registration := pluginRegistration(true)
	if !registration.Capabilities.ModelProvider {
		t.Fatal("ModelProvider = false, want true")
	}
	if len(registration.Capabilities.ModelProviderIdentifiers) != 1 || registration.Capabilities.ModelProviderIdentifiers[0] != "vertex" {
		t.Fatalf("ModelProviderIdentifiers = %#v", registration.Capabilities.ModelProviderIdentifiers)
	}
}

func TestPluginRegistrationWithoutNativeCandidatesFeatureIsInert(t *testing.T) {
	registration := pluginRegistration(false)
	if registration.Capabilities.ModelProvider {
		t.Fatal("ModelProvider = true, want false")
	}
	if len(registration.Capabilities.ModelProviderIdentifiers) != 0 {
		t.Fatalf("ModelProviderIdentifiers = %#v, want empty", registration.Capabilities.ModelProviderIdentifiers)
	}
}

func TestLifecycleHostFeatureControlsRegistration(t *testing.T) {
	tests := []struct {
		name          string
		hostFeatures  []string
		wantProvider  bool
		wantVertexIDs bool
	}{
		{name: "feature present", hostFeatures: []string{pluginabi.HostFeatureModelProviderNativeCandidates}, wantProvider: true, wantVertexIDs: true},
		{name: "feature absent"},
		{name: "unrelated feature", hostFeatures: []string{"unrelated-feature"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rawRequest, errMarshal := json.Marshal(lifecycleRequest{HostFeatures: tt.hostFeatures})
			if errMarshal != nil {
				t.Fatalf("json.Marshal() error = %v", errMarshal)
			}
			for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
				t.Run(method, func(t *testing.T) {
					rawResponse, errHandle := handleMethod(method, rawRequest)
					if errHandle != nil {
						t.Fatalf("handleMethod() error = %v", errHandle)
					}
					var response envelope
					if errUnmarshal := json.Unmarshal(rawResponse, &response); errUnmarshal != nil {
						t.Fatalf("json.Unmarshal(envelope) error = %v", errUnmarshal)
					}
					var got registration
					if errUnmarshal := json.Unmarshal(response.Result, &got); errUnmarshal != nil {
						t.Fatalf("json.Unmarshal(registration) error = %v", errUnmarshal)
					}
					if got.Capabilities.ModelProvider != tt.wantProvider {
						t.Fatalf("ModelProvider = %v, want %v", got.Capabilities.ModelProvider, tt.wantProvider)
					}
					if (len(got.Capabilities.ModelProviderIdentifiers) == 1 && got.Capabilities.ModelProviderIdentifiers[0] == "vertex") != tt.wantVertexIDs {
						t.Fatalf("ModelProviderIdentifiers = %#v", got.Capabilities.ModelProviderIdentifiers)
					}
				})
			}
		})
	}
}
