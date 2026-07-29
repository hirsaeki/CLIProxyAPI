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

func TestDiscoveryUsesDocumentationAsAuthoritativeCatalog(t *testing.T) {
	const authoritativeFixture = `<!doctype html><table>
<thead><tr><th></th><th>US (us)</th></tr></thead>
<tbody>
<tr><td><a>Gemini Multi</a><code>(gemini-multi)</code></td><td aria-label="Supported"></td></tr>
<tr><td><a>Gemini 3.1 Pro</a><code>(gemini-3.1-pro-preview)</code></td><td aria-label="Supported"></td></tr>
<tr><td><a>Gemini Docs Only</a><code>(gemini-docs-only)</code></td><td aria-label="Supported"></td></tr>
</tbody></table>`
	service := &discoveryService{cache: newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(authoritativeFixture)}, nil
	})}
	request := authModelRPCRequest{
		AuthModelRequest: pluginapi.AuthModelRequest{
			AuthID:       "vertex-auth",
			AuthProvider: "vertex",
			Metadata:     map[string]any{"location": "us"},
			CandidateModels: []pluginapi.ModelInfo{
				{ID: "gemini-multi", DisplayName: "Multi", SupportedParameters: []string{"temperature"}, Thinking: &pluginapi.ThinkingSupport{Min: 128, Max: 32768}},
				{ID: "gemini-3.1-pro", DisplayName: "Gemini 3.1 Pro", InputTokenLimit: 1048576, Thinking: &pluginapi.ThinkingSupport{Min: 128, Max: 32768}},
				{ID: "gemini-unsupported", DisplayName: "Unsupported"},
			},
		},
		HostCallbackID: "callback",
	}

	response, errModels := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: true})
	if errModels != nil {
		t.Fatalf("modelsForAuth() error = %v", errModels)
	}
	if response.Provider != "vertex" || len(response.Models) != 3 {
		t.Fatalf("response = %#v", response)
	}
	exact := response.Models[0]
	if exact.ID != "gemini-multi" || exact.DisplayName != "Multi" || exact.Thinking == nil || exact.Thinking.Max != 32768 {
		t.Fatalf("exact model metadata = %#v", exact)
	}
	preview := response.Models[1]
	if preview.ID != "gemini-3.1-pro-preview" || preview.Name != "models/gemini-3.1-pro-preview" || preview.DisplayName != "Gemini 3.1 Pro Preview" || preview.InputTokenLimit != 1048576 || preview.Thinking == nil || preview.Thinking.Max != 32768 {
		t.Fatalf("preview model metadata = %#v", preview)
	}
	docsOnly := response.Models[2]
	if docsOnly.ID != "gemini-docs-only" || docsOnly.Name != "models/gemini-docs-only" || docsOnly.DisplayName != "Gemini Docs Only" || docsOnly.Object != "model" || docsOnly.OwnedBy != "google" || docsOnly.Type != "gemini" {
		t.Fatalf("docs-only model metadata = %#v", docsOnly)
	}
	if docsOnly.Description != "" || docsOnly.InputTokenLimit != 0 || docsOnly.OutputTokenLimit != 0 || docsOnly.Thinking != nil || len(docsOnly.SupportedGenerationMethods) != 0 || len(docsOnly.SupportedInputModalities) != 0 || len(docsOnly.SupportedOutputModalities) != 0 {
		t.Fatalf("docs-only model has invented capability metadata = %#v", docsOnly)
	}
}

func TestDiscoveryFailurePolicy(t *testing.T) {
	type capturedLog struct {
		callbackID string
		level      string
		message    string
		fields     map[string]any
	}
	var logs []capturedLog
	service := &discoveryService{cache: newMatrixCache(func(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
		return pluginapi.HTTPResponse{}, errors.New("network down")
	}), log: func(callbackID, level, message string, fields map[string]any) {
		logs = append(logs, capturedLog{callbackID: callbackID, level: level, message: message, fields: fields})
	}}
	request := authModelRPCRequest{AuthModelRequest: pluginapi.AuthModelRequest{
		Metadata:        map[string]any{"location": "us"},
		CandidateModels: []pluginapi.ModelInfo{{ID: "gemini-candidate"}},
	}, HostCallbackID: "callback"}

	openResponse, errOpen := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: true})
	if errOpen != nil || len(openResponse.Models) != 1 {
		t.Fatalf("fail-open response = %#v, %v", openResponse, errOpen)
	}
	closedResponse, errClosed := service.modelsForAuth(request, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour, FailOpen: false})
	if errClosed != nil || len(closedResponse.Models) != 0 {
		t.Fatalf("fail-closed response = %#v, %v", closedResponse, errClosed)
	}
	if len(logs) != 2 || logs[0].callbackID != "callback" || logs[0].level != "warn" || logs[0].message != "vertex region model catalog unavailable" || logs[0].fields["location"] != "us" {
		t.Fatalf("failure logs = %#v", logs)
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
