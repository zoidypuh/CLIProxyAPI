package helps

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

func TestDefaultClaudeDeviceProfileUpgradesFromLocalCLI(t *testing.T) {
	resetLocalClaudeVersionCacheForTest()
	t.Cleanup(resetLocalClaudeVersionCacheForTest)
	t.Setenv("CLAUDE_CODE_VERSION", "2.1.126")

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version:        "2.1.124",
			UserAgent:      "claude-cli/2.1.124 (external, cli)",
			PackageVersion: "0.81.0",
			RuntimeVersion: "v24.3.0",
			OS:             "Linux",
			Arch:           "x64",
		},
	}

	profile := defaultClaudeDeviceProfile(cfg)
	if got, want := profile.UserAgent, "claude-cli/2.1.126 (external, cli)"; got != want {
		t.Fatalf("UserAgent = %q, want %q", got, want)
	}
	if got := DefaultClaudeVersion(cfg); got != "2.1.126" {
		t.Fatalf("DefaultClaudeVersion = %q, want 2.1.126", got)
	}
}

func TestDefaultClaudeDeviceProfileDoesNotDowngradeFromLocalCLI(t *testing.T) {
	resetLocalClaudeVersionCacheForTest()
	t.Cleanup(resetLocalClaudeVersionCacheForTest)
	t.Setenv("CLAUDE_CODE_VERSION", "2.1.120")

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			Version:   "2.1.124",
			UserAgent: "claude-cli/2.1.124 (external, cli)",
		},
	}

	profile := defaultClaudeDeviceProfile(cfg)
	if got, want := profile.UserAgent, "claude-cli/2.1.124 (external, cli)"; got != want {
		t.Fatalf("UserAgent = %q, want %q", got, want)
	}
}

func TestDefaultClaudeDeviceProfileKeepsCustomUserAgent(t *testing.T) {
	resetLocalClaudeVersionCacheForTest()
	t.Cleanup(resetLocalClaudeVersionCacheForTest)
	t.Setenv("CLAUDE_CODE_VERSION", "2.1.126")

	cfg := &config.Config{
		ClaudeHeaderDefaults: config.ClaudeHeaderDefaults{
			UserAgent: "my-gateway/1.0",
		},
	}

	profile := defaultClaudeDeviceProfile(cfg)
	if got, want := profile.UserAgent, "my-gateway/1.0"; got != want {
		t.Fatalf("UserAgent = %q, want %q", got, want)
	}
}
