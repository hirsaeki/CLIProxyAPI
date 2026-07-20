package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func credentialLocation(req pluginapi.AuthModelRequest) string {
	if location, okLocation := req.Metadata["location"].(string); okLocation {
		if normalized := normalizeLocation(location); normalized != "" {
			return normalized
		}
	}
	if len(req.StorageJSON) > 0 {
		var storage struct {
			Location string `json:"location"`
		}
		if errUnmarshal := json.Unmarshal(req.StorageJSON, &storage); errUnmarshal == nil {
			if normalized := normalizeLocation(storage.Location); normalized != "" {
				return normalized
			}
		}
	}
	return "us-central1"
}

func normalizeLocation(location string) string {
	return strings.ToLower(strings.TrimSpace(location))
}
