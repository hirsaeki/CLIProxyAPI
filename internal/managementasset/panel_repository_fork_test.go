package managementasset

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestManagementPanelRepositoryForkDefaultAndOverrides(t *testing.T) {
	const forkRelease = "https://api.github.com/repos/hirsaeki/Cli-Proxy-API-Management-Center/releases/latest"
	const upstream = "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
	const upstreamRelease = "https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/latest"
	const custom = "https://api.github.com/repos/example/custom-panel/releases/latest"

	tests := []struct {
		name       string
		configYAML string
		want       string
	}{
		{"omitted", "{}\n", forkRelease},
		{"blank", "remote-management:\n  panel-github-repository: '  '\n", forkRelease},
		{"explicit upstream", "remote-management:\n  panel-github-repository: " + upstream + "\n", upstreamRelease},
		{"explicit custom API URL", "remote-management:\n  panel-github-repository: " + custom + "\n", custom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.ParseConfigBytes([]byte(tt.configYAML))
			if err != nil {
				t.Fatal(err)
			}
			if got := resolveReleaseURL(cfg.RemoteManagement.PanelGitHubRepository); got != tt.want {
				t.Fatalf("resolved release = %q, want %q", got, tt.want)
			}
		})
	}

	for _, repo := range []string{"", "not a GitHub repository"} {
		if got := resolveReleaseURL(repo); got != forkRelease {
			t.Errorf("resolveReleaseURL(%q) = %q, want %q", repo, got, forkRelease)
		}
	}
}
