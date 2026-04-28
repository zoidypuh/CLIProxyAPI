package api

import (
	"fmt"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

const selectedAuthIDGinKey = "CLIPROXY_SELECTED_AUTH_ID"

func (s *Server) usageLimitWarning(c *gin.Context, _ string, modelName string, _ []byte) string {
	if s == nil || s.cfg == nil || s.handlers == nil || s.handlers.AuthManager == nil {
		return ""
	}
	warningCfg := config.NormalizeUsageLimitWarningConfig(s.cfg.UsagePercentCalibration.Warning)
	if !warningCfg.Enabled || len(s.cfg.UsagePercentCalibration.Calibrations) == 0 {
		return ""
	}
	selectedAuthID := selectedAuthIDFromGin(c)
	if selectedAuthID == "" {
		return ""
	}
	auth, ok := s.handlers.AuthManager.GetByID(selectedAuthID)
	if !ok || auth == nil {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	authID := strings.TrimSpace(auth.ID)
	authIndex := strings.TrimSpace(auth.EnsureIndex())
	model := strings.TrimSpace(modelName)
	if provider == "" || model == "" {
		return ""
	}

	calibration := config.FindBestUsagePercentCalibrationForAuthIndex(
		s.cfg.UsagePercentCalibration.Calibrations,
		provider,
		model,
		authID,
		authIndex,
		"",
	)
	if calibration == nil {
		return ""
	}
	tokensPerPercent := tokensPerPercentForWarningWindow(*calibration, warningCfg.Window)
	if tokensPerPercent <= 0 {
		return ""
	}

	snapshot := usage.GetRequestStatistics().SnapshotV2()
	currentValue := usageValueForCalibration(snapshot, *calibration, provider, model, authID, authIndex)
	percent := currentValue / tokensPerPercent
	if percent < warningCfg.ThresholdPercent {
		return ""
	}
	if percent > 100 {
		percent = 100
	}
	remaining := math.Max(0, 100-percent)
	return formatUsageLimitWarning(warningCfg, percent, remaining)
}

func selectedAuthIDFromGin(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw, exists := c.Get(selectedAuthIDGinKey)
	if !exists {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

func tokensPerPercentForWarningWindow(calibration config.UsagePercentCalibration, window string) float64 {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "five-hour":
		if calibration.FiveHourTokensPerPercent > 0 {
			return calibration.FiveHourTokensPerPercent
		}
	default:
		if calibration.WeeklyTokensPerPercent > 0 {
			return calibration.WeeklyTokensPerPercent
		}
	}
	return calibration.TokensPerPercent
}

func usageValueForCalibration(snapshot usage.StatisticsSnapshotV2, calibration config.UsagePercentCalibration, provider string, model string, authID string, authIndex string) float64 {
	switch calibration.TokenKind {
	case usage.TokenKindOutputTokens:
		return float64(usage.OutputTokensForCalibrationScopeWithAuthIndex(snapshot, provider, model, authID, authIndex, ""))
	case "subscription_score":
		return usage.SubscriptionUsageScoreForCalibrationScope(snapshot, provider, model, authID, authIndex, "")
	case usage.TokenKindWeightedPriceScore:
		return usage.WeightedPriceScoreForCalibrationScope(snapshot, provider, model, authID, authIndex, "")
	}
	return float64(usage.TokensForCalibrationScopeWithAuthIndex(snapshot, provider, model, authID, authIndex, ""))
}

func formatUsageLimitWarning(cfg config.UsageLimitWarningConfig, percent float64, remaining float64) string {
	window := "weekly"
	if cfg.Window == "five-hour" {
		window = "5h"
	}
	message := strings.TrimSpace(cfg.Message)
	if message == "" {
		return fmt.Sprintf("Warning nearing usage limit: estimated %.0f%% used, %.0f%% remaining in the %s window.", percent, remaining, window)
	}
	replacements := map[string]string{
		"{{percent}}":           fmt.Sprintf("%.0f", percent),
		"{{used_percent}}":      fmt.Sprintf("%.0f", percent),
		"{{remaining_percent}}": fmt.Sprintf("%.0f", remaining),
		"{{window}}":            window,
	}
	for old, newValue := range replacements {
		message = strings.ReplaceAll(message, old, newValue)
	}
	return message
}
