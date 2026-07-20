package main

import (
	"testing"
	"time"
)

func TestDecodePluginConfigDefaults(t *testing.T) {
	cfg, errDecode := decodePluginConfig(nil)
	if errDecode != nil {
		t.Fatalf("decodePluginConfig() error = %v", errDecode)
	}
	if cfg.DocsURL != defaultDocsURL {
		t.Fatalf("DocsURL = %q, want %q", cfg.DocsURL, defaultDocsURL)
	}
	if cfg.CacheTTL != 6*time.Hour {
		t.Fatalf("CacheTTL = %v, want 6h", cfg.CacheTTL)
	}
	if !cfg.FailOpen {
		t.Fatal("FailOpen = false, want true")
	}
}

func TestDecodePluginConfigOverrides(t *testing.T) {
	cfg, errDecode := decodePluginConfig([]byte("docs_url: https://mirror.example/locations\ncache_ttl_seconds: 120\nfail_open: false\n"))
	if errDecode != nil {
		t.Fatalf("decodePluginConfig() error = %v", errDecode)
	}
	if cfg.DocsURL != "https://mirror.example/locations" || cfg.CacheTTL != 2*time.Minute || cfg.FailOpen {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestDecodePluginConfigRejectsInvalidURL(t *testing.T) {
	if _, errDecode := decodePluginConfig([]byte("docs_url: file:///tmp/locations.html\n")); errDecode == nil {
		t.Fatal("decodePluginConfig() error = nil, want invalid URL error")
	}
}
