package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultPluginsDir = "plugins"

// ResolvePluginsDir normalizes the plugin directory for consistent use throughout the app.
// It expands @exe relative to the executable, expands a leading tilde (~) to the user's home
// directory, and defaults empty values to plugins.
func ResolvePluginsDir(pluginsDir string) (string, error) {
	pluginsDir = strings.TrimSpace(pluginsDir)
	if executableRelativePluginsDir(pluginsDir) {
		executablePath, errExecutable := os.Executable()
		if errExecutable != nil {
			return "", fmt.Errorf("resolve plugins directory executable: %w", errExecutable)
		}
		return resolvePluginsDirWithExecutable(pluginsDir, executablePath)
	}
	return resolvePluginsDirWithExecutable(pluginsDir, "")
}

func resolvePluginsDirWithExecutable(pluginsDir, executablePath string) (string, error) {
	pluginsDir = strings.TrimSpace(pluginsDir)
	if pluginsDir == "" {
		pluginsDir = defaultPluginsDir
	}
	if executableRelativePluginsDir(pluginsDir) {
		executablePath = strings.TrimSpace(executablePath)
		if executablePath == "" {
			return "", fmt.Errorf("resolve plugins directory: executable path is empty")
		}
		executableDir := filepath.Dir(executablePath)
		remainder := strings.TrimLeft(pluginsDir[len("@exe"):], "/\\")
		remainder = filepath.FromSlash(strings.ReplaceAll(remainder, "\\", "/"))
		if filepath.IsAbs(remainder) || filepath.VolumeName(remainder) != "" {
			return "", fmt.Errorf("resolve plugins directory: @exe path must be relative")
		}
		resolved := filepath.Clean(filepath.Join(executableDir, remainder))
		relative, errRelative := filepath.Rel(executableDir, resolved)
		if errRelative != nil {
			return "", fmt.Errorf("resolve plugins directory relative path: %w", errRelative)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return "", fmt.Errorf("resolve plugins directory: @exe path escapes executable directory")
		}
		return resolved, nil
	}
	if strings.HasPrefix(pluginsDir, "~") {
		homeDir, errUserHomeDir := os.UserHomeDir()
		if errUserHomeDir != nil {
			return "", fmt.Errorf("resolve plugins directory: %w", errUserHomeDir)
		}
		remainder := strings.TrimPrefix(pluginsDir, "~")
		remainder = strings.TrimLeft(remainder, "/\\")
		if remainder == "" {
			return filepath.Clean(homeDir), nil
		}
		normalized := strings.ReplaceAll(remainder, "\\", "/")
		return filepath.Clean(filepath.Join(homeDir, filepath.FromSlash(normalized))), nil
	}
	return filepath.Clean(pluginsDir), nil
}

func executableRelativePluginsDir(pluginsDir string) bool {
	return pluginsDir == "@exe" || strings.HasPrefix(pluginsDir, "@exe/") || strings.HasPrefix(pluginsDir, `@exe\`)
}

// ResolvePluginsDir resolves and stores the effective plugin directory.
func (cfg *Config) ResolvePluginsDir() error {
	if cfg == nil {
		return nil
	}
	pluginsDir, errResolvePluginsDir := ResolvePluginsDir(cfg.Plugins.Dir)
	if errResolvePluginsDir != nil {
		return errResolvePluginsDir
	}
	cfg.Plugins.Dir = pluginsDir
	return nil
}
