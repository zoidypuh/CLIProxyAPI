package management

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestUsagePercentCalibrationStartStop(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: round-robin\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(config): %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	h := NewHandler(cfg, configPath, nil)

	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.4",
		AuthID:      "codex-auth",
		App:         "codex",
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 0, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			TotalTokens: 100,
		},
	})
	h.SetUsageStatistics(stats)

	startRec := httptest.NewRecorder()
	startCtx, _ := gin.CreateTestContext(startRec)
	startCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/start", strings.NewReader(`{
		"provider":"codex",
		"model":"gpt-5.4",
		"auth_id":"codex-auth",
		"app":"Codex",
		"current_percent":10
	}`))
	startCtx.Request.Header.Set("Content-Type", "application/json")
	h.StartUsagePercentCalibration(startCtx)
	if startRec.Code != http.StatusOK {
		t.Fatalf("Start status = %d, want %d, body=%s", startRec.Code, http.StatusOK, startRec.Body.String())
	}
	if h.cfg.UsagePercentCalibration.Active == nil {
		t.Fatalf("active calibration = nil, want non-nil")
	}
	if h.cfg.UsagePercentCalibration.Active.StartTokens != 100 {
		t.Fatalf("start_tokens = %d, want 100", h.cfg.UsagePercentCalibration.Active.StartTokens)
	}

	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.4",
		AuthID:      "codex-auth",
		App:         "codex",
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			TotalTokens: 500,
		},
	})

	stopRec := httptest.NewRecorder()
	stopCtx, _ := gin.CreateTestContext(stopRec)
	stopCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/stop", strings.NewReader(`{
		"current_percent":10.5
	}`))
	stopCtx.Request.Header.Set("Content-Type", "application/json")
	h.StopUsagePercentCalibration(stopCtx)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("Stop status = %d, want %d, body=%s", stopRec.Code, http.StatusOK, stopRec.Body.String())
	}

	var stopBody struct {
		Calibration config.UsagePercentCalibration `json:"calibration"`
	}
	if err := json.Unmarshal(stopRec.Body.Bytes(), &stopBody); err != nil {
		t.Fatalf("json.Unmarshal(stop): %v", err)
	}
	if stopBody.Calibration.SampleTokens != 500 {
		t.Fatalf("sample_tokens = %d, want 500", stopBody.Calibration.SampleTokens)
	}
	if math.Abs(stopBody.Calibration.TokensPerPercent-1000) > 0.0001 {
		t.Fatalf("tokens_per_percent = %v, want 1000", stopBody.Calibration.TokensPerPercent)
	}
	if h.cfg.UsagePercentCalibration.Active != nil {
		t.Fatalf("active calibration = %+v, want nil", h.cfg.UsagePercentCalibration.Active)
	}

	reloaded, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(reloaded): %v", err)
	}
	if len(reloaded.UsagePercentCalibration.Calibrations) != 1 {
		t.Fatalf("reloaded calibrations len = %d, want 1", len(reloaded.UsagePercentCalibration.Calibrations))
	}
	if reloaded.UsagePercentCalibration.Calibrations[0].Provider != "codex" {
		t.Fatalf("reloaded provider = %q, want %q", reloaded.UsagePercentCalibration.Calibrations[0].Provider, "codex")
	}
}

func TestUsagePercentCalibrationSubscriptionScore(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: round-robin\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(config): %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	h := NewHandler(cfg, configPath, nil)
	stats := usage.NewRequestStatistics()
	h.SetUsageStatistics(stats)

	startRec := httptest.NewRecorder()
	startCtx, _ := gin.CreateTestContext(startRec)
	startCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/start", strings.NewReader(`{
		"provider":"codex",
		"model":"gpt-5.5",
		"auth_index":"7",
		"app":"codex",
		"current_percent":10,
		"current_five_hour_percent":20,
		"current_weekly_percent":10,
		"token_kind":"subscription_score",
		"target_score":50000,
		"max_duration_seconds":600
	}`))
	startCtx.Request.Header.Set("Content-Type", "application/json")
	h.StartUsagePercentCalibration(startCtx)
	if startRec.Code != http.StatusOK {
		t.Fatalf("Start status = %d, want %d, body=%s", startRec.Code, http.StatusOK, startRec.Body.String())
	}

	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.5",
		AuthIndex:   "7",
		App:         "codex",
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			InputTokens:  1000,
			OutputTokens: 100,
			CachedTokens: 400,
			TotalTokens:  1100,
		},
	})

	stopRec := httptest.NewRecorder()
	stopCtx, _ := gin.CreateTestContext(stopRec)
	stopCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/stop", strings.NewReader(`{
		"current_percent":11,
		"current_five_hour_percent":22,
		"current_weekly_percent":11
	}`))
	stopCtx.Request.Header.Set("Content-Type", "application/json")
	h.StopUsagePercentCalibration(stopCtx)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("Stop status = %d, want %d, body=%s", stopRec.Code, http.StatusOK, stopRec.Body.String())
	}

	var stopBody struct {
		Calibration config.UsagePercentCalibration `json:"calibration"`
	}
	if err := json.Unmarshal(stopRec.Body.Bytes(), &stopBody); err != nil {
		t.Fatalf("json.Unmarshal(stop): %v", err)
	}
	if math.Abs(stopBody.Calibration.SampleScore-190) > 0.0001 {
		t.Fatalf("sample_score = %v, want 190", stopBody.Calibration.SampleScore)
	}
	if math.Abs(stopBody.Calibration.WeeklyTokensPerPercent-190) > 0.0001 {
		t.Fatalf("weekly_tokens_per_percent = %v, want 190", stopBody.Calibration.WeeklyTokensPerPercent)
	}
	if math.Abs(stopBody.Calibration.FiveHourTokensPerPercent-95) > 0.0001 {
		t.Fatalf("five_hour_tokens_per_percent = %v, want 95", stopBody.Calibration.FiveHourTokensPerPercent)
	}
	if stopBody.Calibration.WeeklyTotalTokens != 19000 {
		t.Fatalf("weekly_total_tokens = %d, want 19000", stopBody.Calibration.WeeklyTotalTokens)
	}
	if stopBody.Calibration.FiveHourTotalTokens != 9500 {
		t.Fatalf("five_hour_total_tokens = %d, want 9500", stopBody.Calibration.FiveHourTotalTokens)
	}
}

func TestUsagePercentCalibrationFallsBackWhenAppScopeRegresses(t *testing.T) {
	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.5",
		AuthIndex:   "7",
		App:         "mara",
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			TotalTokens: 150,
		},
	})

	session := &config.UsagePercentCalibrationSession{
		Provider:    "codex",
		Model:       "gpt-5.5",
		AuthIndex:   "7",
		App:         "codex",
		StartTokens: 100,
		StartScore:  100,
	}
	currentUsage := currentCalibrationUsage(stats.SnapshotV2(), session)
	if currentUsage.tokens != 150 {
		t.Fatalf("current_tokens = %d, want fallback total 150", currentUsage.tokens)
	}
	if currentUsage.score != 150 {
		t.Fatalf("current_score = %v, want fallback total 150", currentUsage.score)
	}
}

func TestUsagePercentCalibrationStartClearsStaleAppScope(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: round-robin\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(config): %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}
	h := NewHandler(cfg, configPath, nil)
	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5.5",
		AuthIndex:   "7",
		App:         "unknown",
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			InputTokens:  1000,
			OutputTokens: 100,
			CachedTokens: 400,
			TotalTokens:  1100,
		},
	})
	h.SetUsageStatistics(stats)

	startRec := httptest.NewRecorder()
	startCtx, _ := gin.CreateTestContext(startRec)
	startCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/start", strings.NewReader(`{
		"provider":"codex",
		"model":"gpt-5.5",
		"auth_index":"7",
		"app":"codex",
		"current_percent":10,
		"token_kind":"subscription_score"
	}`))
	startCtx.Request.Header.Set("Content-Type", "application/json")
	h.StartUsagePercentCalibration(startCtx)
	if startRec.Code != http.StatusOK {
		t.Fatalf("Start status = %d, want %d, body=%s", startRec.Code, http.StatusOK, startRec.Body.String())
	}
	if h.cfg.UsagePercentCalibration.Active == nil {
		t.Fatalf("active calibration = nil, want non-nil")
	}
	if h.cfg.UsagePercentCalibration.Active.App != "" {
		t.Fatalf("active app = %q, want empty fallback scope", h.cfg.UsagePercentCalibration.Active.App)
	}
	if math.Abs(h.cfg.UsagePercentCalibration.Active.StartScore-190) > 0.0001 {
		t.Fatalf("start_score = %v, want broad score 190", h.cfg.UsagePercentCalibration.Active.StartScore)
	}
}

func TestUsagePercentCalibrationAutomaticCandidatesOnlyEnabled(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	enabledPath := filepath.Join(authDir, "enabled.json")
	disabledPath := filepath.Join(authDir, "disabled.json")
	if err := os.WriteFile(enabledPath, []byte(`{"type":"codex","email":"enabled@example.com"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(enabled): %v", err)
	}
	if err := os.WriteFile(disabledPath, []byte(`{"type":"codex","email":"disabled@example.com","disabled":true}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(disabled): %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	enabledID := "enabled-" + strings.ReplaceAll(t.Name(), "/", "-")
	disabledID := "disabled-" + strings.ReplaceAll(t.Name(), "/", "-")
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         enabledID,
		Provider:   "codex",
		FileName:   "enabled.json",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"path": enabledPath},
		Metadata:   map[string]any{"type": "codex"},
	}); err != nil {
		t.Fatalf("Register(enabled): %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         disabledID,
		Provider:   "codex",
		FileName:   "disabled.json",
		Status:     coreauth.StatusDisabled,
		Disabled:   true,
		Attributes: map[string]string{"path": disabledPath},
		Metadata:   map[string]any{"type": "codex", "disabled": true},
	}); err != nil {
		t.Fatalf("Register(disabled): %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(enabledID, "codex", []*registry.ModelInfo{{ID: "gpt-auto-candidates"}})
	registry.GetGlobalRegistry().RegisterClient(disabledID, "codex", []*registry.ModelInfo{{ID: "gpt-disabled"}})

	h := NewHandler(&config.Config{}, "", manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage/percent-calibration/automatic", nil)

	h.GetUsagePercentCalibrationAutomatic(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Candidates []struct {
			AuthFile map[string]any   `json:"auth_file"`
			Models   []map[string]any `json:"models"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(body.Candidates) != 1 {
		t.Fatalf("candidates len = %d, want 1; body=%s", len(body.Candidates), rec.Body.String())
	}
	if got := body.Candidates[0].AuthFile["id"]; got != enabledID {
		t.Fatalf("candidate id = %v, want %s", got, enabledID)
	}
	if len(body.Candidates[0].Models) != 1 || body.Candidates[0].Models[0]["id"] != "gpt-auto-candidates" {
		t.Fatalf("models = %+v, want gpt-auto-candidates", body.Candidates[0].Models)
	}
}

func TestUsagePercentCalibrationAutomaticCandidatesUseObservedUsageModels(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	authDir := t.TempDir()
	authPath := filepath.Join(authDir, "observed.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex","email":"observed@example.com"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(auth): %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	authID := "observed-" + strings.ReplaceAll(t.Name(), "/", "-")
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         authID,
		Provider:   "codex",
		FileName:   "observed.json",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"path": authPath},
		Metadata:   map[string]any{"type": "codex"},
	}); err != nil {
		t.Fatalf("Register(observed): %v", err)
	}

	h := NewHandler(&config.Config{}, "", manager)
	stats := usage.NewRequestStatistics()
	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-observed-usage",
		AuthID:      authID,
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			InputTokens: 100,
			TotalTokens: 100,
		},
	})
	h.SetUsageStatistics(stats)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/usage/percent-calibration/automatic", nil)

	h.GetUsagePercentCalibrationAutomatic(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Candidates []struct {
			AuthFile map[string]any   `json:"auth_file"`
			Models   []map[string]any `json:"models"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(body.Candidates) != 1 {
		t.Fatalf("candidates len = %d, want 1; body=%s", len(body.Candidates), rec.Body.String())
	}
	if len(body.Candidates[0].Models) != 1 || body.Candidates[0].Models[0]["id"] != "gpt-observed-usage" {
		t.Fatalf("models = %+v, want gpt-observed-usage", body.Candidates[0].Models)
	}
}

func TestUsagePercentCalibrationAutomaticCodexOutputTokensAndBorder(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: round-robin\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(config): %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}

	authDir := t.TempDir()
	authPath := filepath.Join(authDir, "codex.json")
	if err := os.WriteFile(authPath, []byte(`{"type":"codex","email":"auto@example.com"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(auth): %v", err)
	}
	manager := coreauth.NewManager(nil, nil, nil)
	authID := "auto-output-" + strings.ReplaceAll(t.Name(), "/", "-")
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         authID,
		Provider:   "codex",
		FileName:   "codex.json",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"path": authPath},
		Metadata:   map[string]any{"type": "codex"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(authID, "codex", []*registry.ModelInfo{{ID: "gpt-auto-output"}})

	h := NewHandler(cfg, configPath, manager)
	stats := usage.NewRequestStatistics()
	h.SetUsageStatistics(stats)

	startRec := httptest.NewRecorder()
	startCtx, _ := gin.CreateTestContext(startRec)
	startCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/automatic/start", strings.NewReader(`{
		"auth_id":"`+authID+`",
		"model":"gpt-auto-output",
		"current_percent":10
	}`))
	startCtx.Request.Header.Set("Content-Type", "application/json")
	h.StartUsagePercentCalibrationAutomatic(startCtx)
	if startRec.Code != http.StatusOK {
		t.Fatalf("Start status = %d, want %d, body=%s", startRec.Code, http.StatusOK, startRec.Body.String())
	}
	if h.cfg.UsagePercentCalibration.Active == nil {
		t.Fatalf("active calibration = nil, want non-nil")
	}
	if h.cfg.UsagePercentCalibration.Active.TokenKind != usage.TokenKindOutputTokens {
		t.Fatalf("token_kind = %q, want %q", h.cfg.UsagePercentCalibration.Active.TokenKind, usage.TokenKindOutputTokens)
	}
	authIndex := h.cfg.UsagePercentCalibration.Active.AuthIndex

	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-auto-output",
		AuthID:      authID,
		AuthIndex:   authIndex,
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 5, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			OutputTokens: 4999,
			TotalTokens:  4999,
		},
	})

	earlyStopRec := httptest.NewRecorder()
	earlyStopCtx, _ := gin.CreateTestContext(earlyStopRec)
	earlyStopCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/automatic/stop", strings.NewReader(`{
		"current_percent":11
	}`))
	earlyStopCtx.Request.Header.Set("Content-Type", "application/json")
	h.StopUsagePercentCalibrationAutomatic(earlyStopCtx)
	if earlyStopRec.Code != http.StatusConflict {
		t.Fatalf("Early stop status = %d, want %d, body=%s", earlyStopRec.Code, http.StatusConflict, earlyStopRec.Body.String())
	}

	stats.Record(nil, coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-auto-output",
		AuthID:      authID,
		AuthIndex:   authIndex,
		APIKey:      "client-a",
		RequestedAt: time.Date(2026, 4, 10, 3, 6, 0, 0, time.UTC),
		Detail: coreusage.Detail{
			OutputTokens: 1,
			TotalTokens:  1,
		},
	})

	stopRec := httptest.NewRecorder()
	stopCtx, _ := gin.CreateTestContext(stopRec)
	stopCtx.Request = httptest.NewRequest(http.MethodPost, "/v0/management/usage/percent-calibration/automatic/stop", strings.NewReader(`{
		"current_percent":11
	}`))
	stopCtx.Request.Header.Set("Content-Type", "application/json")
	h.StopUsagePercentCalibrationAutomatic(stopCtx)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("Stop status = %d, want %d, body=%s", stopRec.Code, http.StatusOK, stopRec.Body.String())
	}

	var stopBody struct {
		Calibration config.UsagePercentCalibration `json:"calibration"`
	}
	if err := json.Unmarshal(stopRec.Body.Bytes(), &stopBody); err != nil {
		t.Fatalf("json.Unmarshal(stop): %v", err)
	}
	if stopBody.Calibration.SampleTokens != 5000 {
		t.Fatalf("sample_tokens = %d, want 5000", stopBody.Calibration.SampleTokens)
	}
	if stopBody.Calibration.SampleOutputTokens != 5000 {
		t.Fatalf("sample_output_tokens = %d, want 5000", stopBody.Calibration.SampleOutputTokens)
	}
	if math.Abs(stopBody.Calibration.TokensPerPercent-5000) > 0.0001 {
		t.Fatalf("tokens_per_percent = %v, want 5000", stopBody.Calibration.TokensPerPercent)
	}
	if stopBody.Calibration.TokenKind != usage.TokenKindOutputTokens {
		t.Fatalf("token_kind = %q, want %q", stopBody.Calibration.TokenKind, usage.TokenKindOutputTokens)
	}
}
