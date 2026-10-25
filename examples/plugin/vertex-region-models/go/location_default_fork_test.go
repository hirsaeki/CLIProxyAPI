package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestDiscoveryDefaultLocationMatchesNativeGlobalFork(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  pluginapi.AuthModelRequest
		want string
	}{
		{name: "omitted", want: "gemini-global"},
		{name: "blank", req: pluginapi.AuthModelRequest{Metadata: map[string]any{"location": "  "}}, want: "gemini-global"},
		{name: "explicit regional", req: pluginapi.AuthModelRequest{Metadata: map[string]any{"location": "us-central1"}}, want: "gemini-regional"},
		{name: "persisted regional", req: pluginapi.AuthModelRequest{StorageJSON: []byte(`{"location":"us-central1"}`)}, want: "gemini-regional"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &discoveryService{cache: newMatrixCache(func(_ string, _ string, _ http.Header) (pluginapi.HTTPResponse, error) {
				return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Body: []byte(matrixFixture)}, nil
			})}
			response, errModels := service.modelsForAuth(authModelRPCRequest{AuthModelRequest: tt.req}, pluginConfig{DocsURL: defaultDocsURL, CacheTTL: time.Hour})
			if errModels != nil || len(response.Models) != 1 || response.Models[0].ID != tt.want {
				t.Fatalf("modelsForAuth() = %#v, %v; want only %s", response, errModels, tt.want)
			}
		})
	}
}
