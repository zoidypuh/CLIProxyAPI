package management

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const automaticCalibrationSampleBorderTokens int64 = 50000

type usageExportPayload struct {
	Version    int                      `json:"version"`
	ExportedAt time.Time                `json:"exported_at"`
	Usage      usage.StatisticsSnapshot `json:"usage"`
}

type usageImportPayload struct {
	Version int                      `json:"version"`
	Usage   usage.StatisticsSnapshot `json:"usage"`
}

// GetUsageStatistics returns the in-memory request statistics snapshot.
func (h *Handler) GetUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

// GetUsageStatisticsV2 returns a UI-friendly usage snapshot grouped by client/provider/auth/model.
func (h *Handler) GetUsageStatisticsV2(c *gin.Context) {
	var snapshot usage.StatisticsSnapshotV2
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.SnapshotV2()
		if h.cfg != nil {
			usage.ApplyUsagePercentCalibrations(&snapshot, h.cfg.UsagePercentCalibration)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"usage":           snapshot,
		"failed_requests": snapshot.FailureCount,
	})
}

type usagePercentCalibrationState struct {
	Active       *usagePercentCalibrationActive   `json:"active,omitempty"`
	Calibrations []config.UsagePercentCalibration `json:"calibrations,omitempty"`
}

type usagePercentCalibrationActive struct {
	config.UsagePercentCalibrationSession
	CurrentTokens int64   `json:"current_tokens"`
	SampleTokens  int64   `json:"sample_tokens"`
	CurrentScore  float64 `json:"current_score,omitempty"`
	SampleScore   float64 `json:"sample_score,omitempty"`
}

type usagePercentCalibrationStartRequest struct {
	Provider               string  `json:"provider"`
	Model                  string  `json:"model"`
	AuthID                 string  `json:"auth_id"`
	AuthIndex              string  `json:"auth_index"`
	App                    string  `json:"app"`
	CurrentPercent         float64 `json:"current_percent"`
	CurrentFiveHourPercent float64 `json:"current_five_hour_percent"`
	CurrentWeeklyPercent   float64 `json:"current_weekly_percent"`
	TokenKind              string  `json:"token_kind"`
	TargetScore            float64 `json:"target_score"`
	MaxDurationSeconds     int64   `json:"max_duration_seconds"`
}

type usagePercentCalibrationStopRequest struct {
	CurrentPercent         float64 `json:"current_percent"`
	CurrentFiveHourPercent float64 `json:"current_five_hour_percent"`
	CurrentWeeklyPercent   float64 `json:"current_weekly_percent"`
}

type usagePercentCalibrationAutomaticState struct {
	SampleBorderTokens int64                                       `json:"sample_border_tokens"`
	ScoreFormula       string                                      `json:"score_formula"`
	Candidates         []usagePercentCalibrationAutomaticCandidate `json:"candidates"`
	Active             *usagePercentCalibrationAutomaticActive     `json:"active,omitempty"`
	Calibrations       []config.UsagePercentCalibration            `json:"calibrations,omitempty"`
}

type usagePercentCalibrationAutomaticCandidate struct {
	AuthFile gin.H   `json:"auth_file"`
	Models   []gin.H `json:"models"`
}

type usagePercentCalibrationAutomaticActive struct {
	config.UsagePercentCalibrationSession
	CurrentTokens      int64   `json:"current_tokens"`
	SampleTokens       int64   `json:"sample_tokens"`
	RemainingTokens    int64   `json:"remaining_tokens"`
	SampleBorderTokens int64   `json:"sample_border_tokens"`
	Ready              bool    `json:"ready"`
	CurrentScore       float64 `json:"current_score,omitempty"`
	SampleScore        float64 `json:"sample_score,omitempty"`
	ScoreFormula       string  `json:"score_formula"`
}

type usagePercentCalibrationAutomaticStartRequest struct {
	Name                   string  `json:"name"`
	AuthID                 string  `json:"auth_id"`
	AuthIndex              string  `json:"auth_index"`
	Model                  string  `json:"model"`
	App                    string  `json:"app"`
	CurrentPercent         float64 `json:"current_percent"`
	CurrentFiveHourPercent float64 `json:"current_five_hour_percent"`
	CurrentWeeklyPercent   float64 `json:"current_weekly_percent"`
}

func (h *Handler) GetUsagePercentCalibration(c *gin.Context) {
	state := usagePercentCalibrationState{}
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusOK, state)
		return
	}
	cfg := config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	state.Calibrations = cfg.Calibrations
	if cfg.Active != nil && h.usageStats != nil {
		snapshot := h.usageStats.SnapshotV2()
		currentTokens, currentScore := currentCalibrationUsage(snapshot, cfg.Active)
		state.Active = &usagePercentCalibrationActive{
			UsagePercentCalibrationSession: *cfg.Active,
			CurrentTokens:                  currentTokens,
			SampleTokens:                   nonNegativeInt64(currentTokens - cfg.Active.StartTokens),
			CurrentScore:                   currentScore,
			SampleScore:                    nonNegativeFloat64(currentScore - cfg.Active.StartScore),
		}
	}
	c.JSON(http.StatusOK, state)
}

// GetUsagePercentCalibrationAutomatic returns UI-ready automatic calibration state.
func (h *Handler) GetUsagePercentCalibrationAutomatic(c *gin.Context) {
	state := usagePercentCalibrationAutomaticState{
		SampleBorderTokens: automaticCalibrationSampleBorderTokens,
		ScoreFormula:       usage.WeightedPriceScoreFormula,
	}
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusOK, state)
		return
	}
	cfg := config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	state.Calibrations = cfg.Calibrations
	state.Candidates = h.automaticCalibrationCandidates()
	if cfg.Active != nil && h.usageStats != nil && cfg.Active.TokenKind == usage.TokenKindWeightedPriceScore {
		snapshot := h.usageStats.SnapshotV2()
		currentTokens, currentScore := currentCalibrationUsage(snapshot, cfg.Active)
		sampleTokens := nonNegativeInt64(currentTokens - cfg.Active.StartTokens)
		sampleScore := nonNegativeFloat64(currentScore - cfg.Active.StartScore)
		remainingTokens := automaticCalibrationSampleBorderTokens - sampleTokens
		if remainingTokens < 0 {
			remainingTokens = 0
		}
		state.Active = &usagePercentCalibrationAutomaticActive{
			UsagePercentCalibrationSession: *cfg.Active,
			CurrentTokens:                  currentTokens,
			SampleTokens:                   sampleTokens,
			RemainingTokens:                remainingTokens,
			SampleBorderTokens:             automaticCalibrationSampleBorderTokens,
			Ready:                          sampleTokens >= automaticCalibrationSampleBorderTokens,
			CurrentScore:                   currentScore,
			SampleScore:                    sampleScore,
			ScoreFormula:                   usage.WeightedPriceScoreFormula,
		}
	}
	c.JSON(http.StatusOK, state)
}

func (h *Handler) StartUsagePercentCalibration(c *gin.Context) {
	if h == nil || h.cfg == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}
	var body usagePercentCalibrationStartRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	session := config.NormalizeUsagePercentCalibrationSession(&config.UsagePercentCalibrationSession{
		Provider:             body.Provider,
		Model:                body.Model,
		AuthID:               body.AuthID,
		AuthIndex:            body.AuthIndex,
		App:                  body.App,
		StartedAt:            time.Now().UTC(),
		StartPercent:         body.CurrentPercent,
		StartFiveHourPercent: firstPositiveFloat(body.CurrentFiveHourPercent, body.CurrentPercent),
		StartWeeklyPercent:   firstPositiveFloat(body.CurrentWeeklyPercent, body.CurrentPercent),
		TokenKind:            body.TokenKind,
		TargetScore:          body.TargetScore,
		MaxDurationSeconds:   body.MaxDurationSeconds,
	})
	if session == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider, model, and non-negative current_percent are required"})
		return
	}
	snapshot := h.usageStats.SnapshotV2()
	session = normalizeCalibrationSessionApp(snapshot, session)
	session.StartTokens, session.StartScore = currentCalibrationUsage(snapshot, session)
	h.cfg.UsagePercentCalibration.Active = session
	h.cfg.UsagePercentCalibration = config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	if err := h.saveConfigLocked(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active": usagePercentCalibrationActive{
			UsagePercentCalibrationSession: *session,
			CurrentTokens:                  session.StartTokens,
			SampleTokens:                   0,
			CurrentScore:                   session.StartScore,
			SampleScore:                    0,
		},
	})
}

// StartUsagePercentCalibrationAutomatic starts a weighted-score calibration for
// a selected enabled auth file and model.
func (h *Handler) StartUsagePercentCalibrationAutomatic(c *gin.Context) {
	if h == nil || h.cfg == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}
	var body usagePercentCalibrationAutomaticStartRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	auth, ok := h.findAutomaticCalibrationAuth(body.Name, body.AuthID, body.AuthIndex)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}
	if !isEnabledFileBackedCalibrationAuth(auth) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth file is not enabled"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	snapshot := h.usageStats.SnapshotV2()
	if !authSupportsCalibrationModel(auth, model, snapshot) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is not available for selected auth file"})
		return
	}
	auth.EnsureIndex()
	session := config.NormalizeUsagePercentCalibrationSession(&config.UsagePercentCalibrationSession{
		Provider:             auth.Provider,
		Model:                model,
		AuthID:               auth.ID,
		AuthIndex:            auth.Index,
		App:                  body.App,
		StartedAt:            time.Now().UTC(),
		StartPercent:         body.CurrentPercent,
		StartFiveHourPercent: firstPositiveFloat(body.CurrentFiveHourPercent, body.CurrentPercent),
		StartWeeklyPercent:   firstPositiveFloat(body.CurrentWeeklyPercent, body.CurrentPercent),
		TokenKind:            usage.TokenKindWeightedPriceScore,
	})
	if session == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled auth, model, and non-negative current_percent are required"})
		return
	}
	session = normalizeCalibrationSessionApp(snapshot, session)
	session.StartTokens, session.StartScore = currentCalibrationUsage(snapshot, session)
	h.cfg.UsagePercentCalibration.Active = session
	h.cfg.UsagePercentCalibration = config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	if err := h.saveConfigLocked(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"active": usagePercentCalibrationAutomaticActive{
			UsagePercentCalibrationSession: *session,
			CurrentTokens:                  session.StartTokens,
			SampleTokens:                   0,
			RemainingTokens:                automaticCalibrationSampleBorderTokens,
			SampleBorderTokens:             automaticCalibrationSampleBorderTokens,
			Ready:                          false,
			CurrentScore:                   session.StartScore,
			SampleScore:                    0,
			ScoreFormula:                   usage.WeightedPriceScoreFormula,
		},
	})
}

func (h *Handler) StopUsagePercentCalibration(c *gin.Context) {
	h.stopUsagePercentCalibration(c, 0, false)
}

// StopUsagePercentCalibrationAutomatic finalizes a weighted-score calibration
// only after at least 50k proxy-recorded tokens have been sampled.
func (h *Handler) StopUsagePercentCalibrationAutomatic(c *gin.Context) {
	h.stopUsagePercentCalibration(c, automaticCalibrationSampleBorderTokens, true)
}

func (h *Handler) stopUsagePercentCalibration(c *gin.Context, minSampleTokens int64, requireAutomatic bool) {
	if h == nil || h.cfg == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}
	active := config.NormalizeUsagePercentCalibrationSession(h.cfg.UsagePercentCalibration.Active)
	if active == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no active calibration"})
		return
	}
	if requireAutomatic && active.TokenKind != usage.TokenKindWeightedPriceScore {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active calibration is not automatic"})
		return
	}
	var body usagePercentCalibrationStopRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	currentFiveHourPercent := firstPositiveFloat(body.CurrentFiveHourPercent, body.CurrentPercent)
	currentWeeklyPercent := firstPositiveFloat(body.CurrentWeeklyPercent, body.CurrentPercent)
	if body.CurrentPercent < active.StartPercent || currentFiveHourPercent < active.StartFiveHourPercent || currentWeeklyPercent < active.StartWeeklyPercent {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current percentages must be greater than or equal to the start percentages"})
		return
	}
	snapshot := h.usageStats.SnapshotV2()
	currentTokens, currentScore := currentCalibrationUsage(snapshot, active)
	sampleTokens := nonNegativeInt64(currentTokens - active.StartTokens)
	sampleScore := nonNegativeFloat64(currentScore - active.StartScore)
	if minSampleTokens > 0 && sampleTokens < minSampleTokens {
		remainingTokens := minSampleTokens - sampleTokens
		if remainingTokens < 0 {
			remainingTokens = 0
		}
		c.JSON(http.StatusConflict, gin.H{
			"error":                "sample token border not reached",
			"sample_tokens":        sampleTokens,
			"sample_border_tokens": minSampleTokens,
			"remaining_tokens":     remainingTokens,
		})
		return
	}
	samplePercent := body.CurrentPercent - active.StartPercent
	fiveHourSamplePercent := currentFiveHourPercent - active.StartFiveHourPercent
	weeklySamplePercent := currentWeeklyPercent - active.StartWeeklyPercent
	if sampleTokens <= 0 && sampleScore <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no tracked tokens were recorded for the selected scope"})
		return
	}
	if samplePercent <= 0 && fiveHourSamplePercent <= 0 && weeklySamplePercent <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one current percent must be greater than its start percent"})
		return
	}
	calibrationValue := float64(sampleTokens)
	if active.TokenKind == "subscription_score" || active.TokenKind == usage.TokenKindWeightedPriceScore {
		calibrationValue = sampleScore
	}
	tokensPerPercent := calibrationValue / samplePercent
	if samplePercent <= 0 {
		tokensPerPercent = 0
	}
	fiveHourTokensPerPercent := calibrationValue / fiveHourSamplePercent
	if fiveHourSamplePercent <= 0 {
		fiveHourTokensPerPercent = 0
	}
	weeklyTokensPerPercent := calibrationValue / weeklySamplePercent
	if weeklySamplePercent <= 0 {
		weeklyTokensPerPercent = 0
	}
	if weeklyTokensPerPercent > 0 {
		tokensPerPercent = weeklyTokensPerPercent
	} else if tokensPerPercent <= 0 {
		tokensPerPercent = fiveHourTokensPerPercent
	}
	if tokensPerPercent <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unable to derive a positive tokens-per-percent ratio"})
		return
	}
	calibration := config.UsagePercentCalibration{
		Provider:                 active.Provider,
		Model:                    active.Model,
		AuthID:                   active.AuthID,
		AuthIndex:                active.AuthIndex,
		App:                      active.App,
		TokenKind:                active.TokenKind,
		TokensPerPercent:         tokensPerPercent,
		FiveHourTokensPerPercent: fiveHourTokensPerPercent,
		WeeklyTokensPerPercent:   weeklyTokensPerPercent,
		FiveHourTotalTokens:      int64(fiveHourTokensPerPercent * 100),
		WeeklyTotalTokens:        int64(weeklyTokensPerPercent * 100),
		SampleTokens:             sampleTokens,
		SampleScore:              sampleScore,
		SamplePercent:            samplePercent,
		FiveHourSamplePercent:    fiveHourSamplePercent,
		WeeklySamplePercent:      weeklySamplePercent,
		StartPercent:             active.StartPercent,
		EndPercent:               body.CurrentPercent,
		StartFiveHourPercent:     active.StartFiveHourPercent,
		EndFiveHourPercent:       currentFiveHourPercent,
		StartWeeklyPercent:       active.StartWeeklyPercent,
		EndWeeklyPercent:         currentWeeklyPercent,
		RecordedAt:               time.Now().UTC(),
	}
	normalized := config.NormalizeUsagePercentCalibrations(append(h.cfg.UsagePercentCalibration.Calibrations, calibration))
	h.cfg.UsagePercentCalibration.Active = nil
	h.cfg.UsagePercentCalibration.Calibrations = normalized
	h.cfg.UsagePercentCalibration = config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	if err := h.saveConfigLocked(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"calibration": calibration,
	})
}

func currentCalibrationUsage(snapshot usage.StatisticsSnapshotV2, session *config.UsagePercentCalibrationSession) (int64, float64) {
	if session == nil {
		return 0, 0
	}
	tokens, score := currentCalibrationUsageForApp(snapshot, session, session.App)
	if strings.TrimSpace(session.App) != "" && calibrationUsageRegressed(session, tokens, score) {
		fallbackTokens, fallbackScore := currentCalibrationUsageForApp(snapshot, session, "")
		if !calibrationUsageRegressed(session, fallbackTokens, fallbackScore) {
			return fallbackTokens, fallbackScore
		}
	}
	return tokens, score
}

func normalizeCalibrationSessionApp(snapshot usage.StatisticsSnapshotV2, session *config.UsagePercentCalibrationSession) *config.UsagePercentCalibrationSession {
	if session == nil || strings.TrimSpace(session.App) == "" {
		return session
	}
	appTokens, appScore := currentCalibrationUsageForApp(snapshot, session, session.App)
	broadTokens, broadScore := currentCalibrationUsageForApp(snapshot, session, "")
	if calibrationUsageValue(session, appTokens, appScore) <= 0 && calibrationUsageValue(session, broadTokens, broadScore) > 0 {
		normalized := *session
		normalized.App = ""
		return &normalized
	}
	return session
}

func currentCalibrationUsageForApp(snapshot usage.StatisticsSnapshotV2, session *config.UsagePercentCalibrationSession, app string) (int64, float64) {
	tokens := usage.TokensForCalibrationScopeWithAuthIndex(snapshot, session.Provider, session.Model, session.AuthID, session.AuthIndex, app)
	switch session.TokenKind {
	case "subscription_score":
		score := usage.SubscriptionUsageScoreForCalibrationScope(snapshot, session.Provider, session.Model, session.AuthID, session.AuthIndex, app)
		return tokens, score
	case usage.TokenKindWeightedPriceScore:
		score := usage.WeightedPriceScoreForCalibrationScope(snapshot, session.Provider, session.Model, session.AuthID, session.AuthIndex, app)
		return tokens, score
	}
	return tokens, float64(tokens)
}

func calibrationUsageRegressed(session *config.UsagePercentCalibrationSession, tokens int64, score float64) bool {
	return calibrationUsageValue(session, tokens, score) < calibrationUsageValue(session, session.StartTokens, session.StartScore)
}

func calibrationUsageValue(session *config.UsagePercentCalibrationSession, tokens int64, score float64) float64 {
	if session != nil && (session.TokenKind == "subscription_score" || session.TokenKind == usage.TokenKindWeightedPriceScore) {
		return score
	}
	return float64(tokens)
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeFloat64(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func (h *Handler) automaticCalibrationCandidates() []usagePercentCalibrationAutomaticCandidate {
	if h == nil || h.authManager == nil {
		return nil
	}
	var snapshot usage.StatisticsSnapshotV2
	if h.usageStats != nil {
		snapshot = h.usageStats.SnapshotV2()
	}
	auths := h.authManager.List()
	candidates := make([]usagePercentCalibrationAutomaticCandidate, 0, len(auths))
	for _, auth := range auths {
		if !isEnabledFileBackedCalibrationAuth(auth) {
			continue
		}
		entry := h.buildAuthFileEntry(auth)
		if entry == nil {
			continue
		}
		candidates = append(candidates, usagePercentCalibrationAutomaticCandidate{
			AuthFile: entry,
			Models:   calibrationModelEntriesForAuth(auth, snapshot),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		nameI, _ := candidates[i].AuthFile["name"].(string)
		nameJ, _ := candidates[j].AuthFile["name"].(string)
		return strings.ToLower(nameI) < strings.ToLower(nameJ)
	})
	return candidates
}

func (h *Handler) findAutomaticCalibrationAuth(name string, authID string, authIndex string) (*coreauth.Auth, bool) {
	if h == nil || h.authManager == nil {
		return nil, false
	}
	name = strings.TrimSpace(name)
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	if authID != "" {
		if auth, ok := h.authManager.GetByID(authID); ok {
			return auth, true
		}
	}
	for _, auth := range h.authManager.List() {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		if authIndex != "" && auth.Index == authIndex {
			return auth, true
		}
		if name != "" && (auth.FileName == name || auth.ID == name) {
			return auth, true
		}
	}
	return nil, false
}

func isEnabledFileBackedCalibrationAuth(auth *coreauth.Auth) bool {
	if auth == nil || auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return false
	}
	path := strings.TrimSpace(authAttribute(auth, "path"))
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var metadata map[string]any
	if err = json.Unmarshal(data, &metadata); err != nil {
		return false
	}
	disabled, _ := metadata["disabled"].(bool)
	return !disabled
}

func authSupportsCalibrationModel(auth *coreauth.Auth, model string, snapshot usage.StatisticsSnapshotV2) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, entry := range calibrationModelEntriesForAuth(auth, snapshot) {
		if strings.TrimSpace(calibrationString(entry["id"])) == model {
			return true
		}
	}
	return false
}

func calibrationModelEntriesForAuth(auth *coreauth.Auth, snapshot usage.StatisticsSnapshotV2) []gin.H {
	if auth == nil {
		return nil
	}
	auth.EnsureIndex()
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	result := make([]gin.H, 0)
	seen := make(map[string]struct{})
	addModel := func(m *registry.ModelInfo) {
		if m == nil {
			return
		}
		id := strings.TrimSpace(m.ID)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		entry := gin.H{"id": id}
		if displayName := strings.TrimSpace(m.DisplayName); displayName != "" {
			entry["display_name"] = displayName
		}
		if modelType := strings.TrimSpace(m.Type); modelType != "" {
			entry["type"] = modelType
		}
		if ownedBy := strings.TrimSpace(m.OwnedBy); ownedBy != "" {
			entry["owned_by"] = ownedBy
		}
		result = append(result, entry)
	}
	addModelID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		result = append(result, gin.H{"id": id})
	}

	reg := registry.GetGlobalRegistry()
	for _, m := range reg.GetModelsForClient(auth.ID) {
		addModel(m)
	}
	for _, entry := range snapshot.Entries {
		if provider != "" && strings.ToLower(strings.TrimSpace(entry.Provider)) != provider {
			continue
		}
		if auth.ID != "" && strings.TrimSpace(entry.AuthID) != auth.ID && auth.Index != "" && strings.TrimSpace(entry.AuthIndex) != auth.Index {
			continue
		}
		addModelID(entry.Model)
	}
	if len(result) == 0 && provider != "" {
		for _, m := range reg.GetAvailableModelsByProvider(provider) {
			addModel(m)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(calibrationString(result[i]["id"])) < strings.ToLower(calibrationString(result[j]["id"]))
	})
	return result
}

func calibrationString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (h *Handler) DeleteUsagePercentCalibrationActive(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}
	h.cfg.UsagePercentCalibration.Active = nil
	h.cfg.UsagePercentCalibration = config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	if err := h.saveConfigLocked(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) DeleteUsagePercentCalibration(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}
	provider := c.Query("provider")
	model := c.Query("model")
	authID := c.Query("auth_id")
	app := c.Query("app")
	if provider == "" || model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider and model are required"})
		return
	}
	filtered := make([]config.UsagePercentCalibration, 0, len(h.cfg.UsagePercentCalibration.Calibrations))
	targetRemoved := false
	for _, entry := range h.cfg.UsagePercentCalibration.Calibrations {
		if entry.Provider == strings.ToLower(strings.TrimSpace(provider)) &&
			entry.Model == strings.TrimSpace(model) &&
			entry.AuthID == strings.TrimSpace(authID) &&
			entry.App == config.NormalizeRoutingAppName(app) {
			targetRemoved = true
			continue
		}
		filtered = append(filtered, entry)
	}
	if !targetRemoved {
		c.JSON(http.StatusNotFound, gin.H{"error": "calibration not found"})
		return
	}
	h.cfg.UsagePercentCalibration.Calibrations = config.NormalizeUsagePercentCalibrations(filtered)
	h.cfg.UsagePercentCalibration = config.NormalizeUsagePercentCalibrationConfig(h.cfg.UsagePercentCalibration)
	if err := h.saveConfigLocked(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ExportUsageStatistics returns a complete usage snapshot for backup/migration.
func (h *Handler) ExportUsageStatistics(c *gin.Context) {
	var snapshot usage.StatisticsSnapshot
	if h != nil && h.usageStats != nil {
		snapshot = h.usageStats.Snapshot()
	}
	c.JSON(http.StatusOK, usageExportPayload{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Usage:      snapshot,
	})
}

// ImportUsageStatistics merges a previously exported usage snapshot into memory.
func (h *Handler) ImportUsageStatistics(c *gin.Context) {
	if h == nil || h.usageStats == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage statistics unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	var payload usageImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	if payload.Version != 0 && payload.Version != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported version"})
		return
	}

	result := h.usageStats.MergeSnapshot(payload.Usage)
	snapshot := h.usageStats.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"added":           result.Added,
		"skipped":         result.Skipped,
		"total_requests":  snapshot.TotalRequests,
		"failed_requests": snapshot.FailureCount,
	})
}
