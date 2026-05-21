package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptionalLangfuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
langfuse:
  enabled: true
  base-url: "http://localhost:3003"
  env-file: "/tmp/langfuse.env"
  environment: "local"
  release: "test-release"
  max-chars: 4096
`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Langfuse.Enabled {
		t.Fatal("expected langfuse enabled")
	}
	if cfg.Langfuse.BaseURL != "http://localhost:3003" {
		t.Fatalf("base-url = %q", cfg.Langfuse.BaseURL)
	}
	if cfg.Langfuse.EnvFile != "/tmp/langfuse.env" {
		t.Fatalf("env-file = %q", cfg.Langfuse.EnvFile)
	}
	if cfg.Langfuse.Environment != "local" {
		t.Fatalf("environment = %q", cfg.Langfuse.Environment)
	}
	if cfg.Langfuse.Release != "test-release" {
		t.Fatalf("release = %q", cfg.Langfuse.Release)
	}
	if cfg.Langfuse.MaxChars != 4096 {
		t.Fatalf("max-chars = %d", cfg.Langfuse.MaxChars)
	}
}
