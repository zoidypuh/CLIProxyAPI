package config

import "testing"

func TestNormalizeUsageLimitWarningConfig(t *testing.T) {
	cfg := NormalizeUsageLimitWarningConfig(UsageLimitWarningConfig{
		Enabled: true,
		Window:  "5h",
		Message: "  warn  ",
	})
	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if cfg.ThresholdPercent != 85 {
		t.Fatalf("ThresholdPercent = %v, want 85", cfg.ThresholdPercent)
	}
	if cfg.Window != "five-hour" {
		t.Fatalf("Window = %q, want five-hour", cfg.Window)
	}
	if cfg.Message != "warn" {
		t.Fatalf("Message = %q, want warn", cfg.Message)
	}
}

func TestNormalizeUsagePercentCalibrationConfigKeepsWarningOnly(t *testing.T) {
	cfg := NormalizeUsagePercentCalibrationConfig(UsagePercentCalibrationConfig{
		Warning: UsageLimitWarningConfig{Enabled: true, ThresholdPercent: 90, Window: "weekly"},
	})
	if !cfg.Warning.Enabled {
		t.Fatalf("warning disabled, want enabled")
	}
	if cfg.Warning.ThresholdPercent != 90 {
		t.Fatalf("threshold = %v, want 90", cfg.Warning.ThresholdPercent)
	}
}
