package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultDocsURL = "https://docs.cloud.google.com/gemini-enterprise-agent-platform/resources/locations"

type pluginConfig struct {
	DocsURL  string
	CacheTTL time.Duration
	FailOpen bool
}

type rawPluginConfig struct {
	DocsURL         string `yaml:"docs_url"`
	CacheTTLSeconds int    `yaml:"cache_ttl_seconds"`
	FailOpen        *bool  `yaml:"fail_open"`
}

func decodePluginConfig(raw []byte) (pluginConfig, error) {
	cfg := pluginConfig{
		DocsURL:  defaultDocsURL,
		CacheTTL: 6 * time.Hour,
		FailOpen: false,
	}
	if len(raw) == 0 {
		return cfg, nil
	}
	var decoded rawPluginConfig
	if errUnmarshal := yaml.Unmarshal(raw, &decoded); errUnmarshal != nil {
		return pluginConfig{}, fmt.Errorf("decode plugin config: %w", errUnmarshal)
	}
	if docsURL := strings.TrimSpace(decoded.DocsURL); docsURL != "" {
		parsed, errParse := url.Parse(docsURL)
		if errParse != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return pluginConfig{}, fmt.Errorf("docs_url must be an absolute HTTP or HTTPS URL")
		}
		cfg.DocsURL = docsURL
	}
	if decoded.CacheTTLSeconds > 0 {
		cfg.CacheTTL = time.Duration(decoded.CacheTTLSeconds) * time.Second
	}
	if decoded.FailOpen != nil {
		cfg.FailOpen = *decoded.FailOpen
	}
	return cfg, nil
}
