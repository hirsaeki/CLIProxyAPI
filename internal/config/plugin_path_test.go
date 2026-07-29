package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePluginsDirWithExecutable(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "install", "cli-proxy-api.exe")
	executableDir := filepath.Dir(executablePath)
	absolutePluginsDir := filepath.Join(t.TempDir(), "absolute-plugins")

	tests := []struct {
		name       string
		pluginsDir string
		want       string
	}{
		{name: "executable directory", pluginsDir: "@exe", want: executableDir},
		{name: "forward slash child", pluginsDir: "@exe/plugins", want: filepath.Join(executableDir, "plugins")},
		{name: "backslash child", pluginsDir: `@exe\plugins`, want: filepath.Join(executableDir, "plugins")},
		{name: "trailing slash", pluginsDir: "@exe/", want: executableDir},
		{name: "empty uses existing default", pluginsDir: "", want: defaultPluginsDir},
		{name: "ordinary relative", pluginsDir: "custom-plugins", want: "custom-plugins"},
		{name: "absolute", pluginsDir: absolutePluginsDir, want: absolutePluginsDir},
		{name: "similar prefix is ordinary relative", pluginsDir: "@executor/plugins", want: filepath.Clean("@executor/plugins")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errResolve := resolvePluginsDirWithExecutable(tt.pluginsDir, executablePath)
			if errResolve != nil {
				t.Fatalf("resolvePluginsDirWithExecutable() error = %v", errResolve)
			}
			if got != tt.want {
				t.Fatalf("resolvePluginsDirWithExecutable() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePluginsDirUsesRunningExecutable(t *testing.T) {
	executablePath, errExecutable := os.Executable()
	if errExecutable != nil {
		t.Fatalf("os.Executable() error = %v", errExecutable)
	}

	got, errResolve := ResolvePluginsDir("@exe/plugins")
	if errResolve != nil {
		t.Fatalf("ResolvePluginsDir() error = %v", errResolve)
	}
	want := filepath.Join(filepath.Dir(executablePath), "plugins")
	if got != want {
		t.Fatalf("ResolvePluginsDir() = %q, want %q", got, want)
	}
}

func TestResolvePluginsDirWithExecutableRejectsEscape(t *testing.T) {
	executablePath := filepath.Join(t.TempDir(), "install", "cli-proxy-api.exe")

	for _, pluginsDir := range []string{
		"@exe/..",
		"@exe/../plugins",
		`@exe\plugins\..\..\outside`,
	} {
		t.Run(pluginsDir, func(t *testing.T) {
			if _, errResolve := resolvePluginsDirWithExecutable(pluginsDir, executablePath); errResolve == nil {
				t.Fatalf("resolvePluginsDirWithExecutable(%q) error = nil, want escape rejection", pluginsDir)
			}
		})
	}
}
