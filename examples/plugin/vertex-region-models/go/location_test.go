package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestCredentialLocation(t *testing.T) {
	tests := []struct {
		name string
		req  pluginapi.AuthModelRequest
		want string
	}{
		{
			name: "metadata wins",
			req: pluginapi.AuthModelRequest{
				Metadata:    map[string]any{"location": " EU "},
				StorageJSON: []byte(`{"location":"us"}`),
			},
			want: "eu",
		},
		{
			name: "persisted credential",
			req:  pluginapi.AuthModelRequest{StorageJSON: []byte(`{"type":"vertex","location":"europe-west4"}`)},
			want: "europe-west4",
		},
		{
			name: "default",
			req:  pluginapi.AuthModelRequest{},
			want: "us-central1",
		},
		{
			name: "invalid storage",
			req:  pluginapi.AuthModelRequest{StorageJSON: []byte(`not-json`)},
			want: "us-central1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := credentialLocation(tt.req); got != tt.want {
				t.Fatalf("credentialLocation() = %q, want %q", got, tt.want)
			}
		})
	}
}
