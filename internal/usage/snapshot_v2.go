package usage

import (
	"sort"
	"strings"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

const (
	TokenKindWeightedPriceScore = "weighted_price_score"
	WeightedPriceScoreFormula   = "5*input_tokens + 30*output_tokens + 0.5*cached_tokens"
)

// StatisticsSnapshotV2 exposes a UI-friendly usage view grouped by client API key,
// downstream app label, upstream provider, selected auth, source, and model.
type StatisticsSnapshotV2 struct {
	TotalRequests int64 `json:"total_requests"`
	SuccessCount  int64 `json:"success_count"`
	FailureCount  int64 `json:"failure_count"`
	TotalTokens   int64 `json:"total_tokens"`

	Clients   map[string]UsageRollupSnapshotV2 `json:"clients"`
	Apps      map[string]UsageRollupSnapshotV2 `json:"apps"`
	Providers map[string]UsageRollupSnapshotV2 `json:"providers"`
	Entries   []UsageEntrySnapshotV2           `json:"entries"`
}

// UsageRollupSnapshotV2 stores a simple request/token rollup.
type UsageRollupSnapshotV2 struct {
	TotalRequests          int64   `json:"total_requests"`
	SuccessCount           int64   `json:"success_count"`
	FailureCount           int64   `json:"failure_count"`
	InputTokens            int64   `json:"input_tokens"`
	OutputTokens           int64   `json:"output_tokens"`
	ReasoningTokens        int64   `json:"reasoning_tokens"`
	CachedTokens           int64   `json:"cached_tokens"`
	TotalTokens            int64   `json:"total_tokens"`
	EstimatedWeeklyPercent float64 `json:"estimated_weekly_percent,omitempty"`
	CalibratedTokens       int64   `json:"calibrated_tokens,omitempty"`
}

// UsageEntrySnapshotV2 is a UI-oriented aggregate row.
type UsageEntrySnapshotV2 struct {
	ClientAPIKey           string    `json:"client_api_key"`
	App                    string    `json:"app,omitempty"`
	Provider               string    `json:"provider"`
	AuthID                 string    `json:"auth_id,omitempty"`
	AuthIndex              string    `json:"auth_index,omitempty"`
	Source                 string    `json:"source,omitempty"`
	Model                  string    `json:"model"`
	TotalRequests          int64     `json:"total_requests"`
	SuccessCount           int64     `json:"success_count"`
	FailureCount           int64     `json:"failure_count"`
	InputTokens            int64     `json:"input_tokens"`
	OutputTokens           int64     `json:"output_tokens"`
	ReasoningTokens        int64     `json:"reasoning_tokens"`
	CachedTokens           int64     `json:"cached_tokens"`
	TotalTokens            int64     `json:"total_tokens"`
	AvgLatencyMs           int64     `json:"avg_latency_ms"`
	LastRequestAt          time.Time `json:"last_request_at,omitempty"`
	EstimatedWeeklyPercent float64   `json:"estimated_weekly_percent,omitempty"`
	CalibratedTokens       int64     `json:"calibrated_tokens,omitempty"`
	TokensPerPercent       float64   `json:"tokens_per_percent,omitempty"`
	CalibrationRecordedAt  time.Time `json:"calibration_recorded_at,omitempty"`
}

type usageEntryKeyV2 struct {
	clientAPIKey string
	app          string
	provider     string
	authID       string
	authIndex    string
	source       string
	model        string
}

type usageEntryAggregateV2 struct {
	entry          UsageEntrySnapshotV2
	totalLatencyMs int64
}

// SnapshotV2 returns a usage view shaped for management UIs.
func (s *RequestStatistics) SnapshotV2() StatisticsSnapshotV2 {
	result := StatisticsSnapshotV2{
		Clients:   make(map[string]UsageRollupSnapshotV2),
		Apps:      make(map[string]UsageRollupSnapshotV2),
		Providers: make(map[string]UsageRollupSnapshotV2),
	}
	if s == nil {
		return result
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	result.TotalRequests = s.totalRequests
	result.SuccessCount = s.successCount
	result.FailureCount = s.failureCount
	result.TotalTokens = s.totalTokens

	entries := make(map[usageEntryKeyV2]*usageEntryAggregateV2)
	for apiName, stats := range s.apis {
		if stats == nil {
			continue
		}
		clientAPIKey := strings.TrimSpace(apiName)
		if clientAPIKey == "" {
			clientAPIKey = "unknown"
		}

		for modelName, modelStatsValue := range stats.Models {
			if modelStatsValue == nil {
				continue
			}
			model := strings.TrimSpace(modelName)
			if model == "" {
				model = "unknown"
			}
			for _, detail := range modelStatsValue.Details {
				tokens := normaliseTokenStats(detail.Tokens)
				app := internalconfig.NormalizeRoutingAppName(detail.App)
				if app == "" {
					app = "unknown"
				}
				provider := strings.TrimSpace(detail.Provider)
				if provider == "" {
					provider = "unknown"
				}
				key := usageEntryKeyV2{
					clientAPIKey: clientAPIKey,
					app:          app,
					provider:     provider,
					authID:       strings.TrimSpace(detail.AuthID),
					authIndex:    strings.TrimSpace(detail.AuthIndex),
					source:       strings.TrimSpace(detail.Source),
					model:        model,
				}
				agg := entries[key]
				if agg == nil {
					agg = &usageEntryAggregateV2{
						entry: UsageEntrySnapshotV2{
							ClientAPIKey: clientAPIKey,
							App:          app,
							Provider:     provider,
							AuthID:       key.authID,
							AuthIndex:    key.authIndex,
							Source:       key.source,
							Model:        model,
						},
					}
					entries[key] = agg
				}
				agg.entry.TotalRequests++
				if detail.Failed {
					agg.entry.FailureCount++
				} else {
					agg.entry.SuccessCount++
				}
				agg.entry.InputTokens += tokens.InputTokens
				agg.entry.OutputTokens += tokens.OutputTokens
				agg.entry.ReasoningTokens += tokens.ReasoningTokens
				agg.entry.CachedTokens += tokens.CachedTokens
				agg.entry.TotalTokens += tokens.TotalTokens
				agg.totalLatencyMs += detail.LatencyMs
				if detail.Timestamp.After(agg.entry.LastRequestAt) {
					agg.entry.LastRequestAt = detail.Timestamp
				}

				providerRollup := result.Providers[provider]
				applyRollupDetail(&providerRollup, detail, tokens)
				result.Providers[provider] = providerRollup

				clientRollup := result.Clients[clientAPIKey]
				applyRollupDetail(&clientRollup, detail, tokens)
				result.Clients[clientAPIKey] = clientRollup

				appRollup := result.Apps[app]
				applyRollupDetail(&appRollup, detail, tokens)
				result.Apps[app] = appRollup
			}
		}
	}

	result.Entries = make([]UsageEntrySnapshotV2, 0, len(entries))
	for _, agg := range entries {
		if agg == nil {
			continue
		}
		if agg.entry.TotalRequests > 0 {
			agg.entry.AvgLatencyMs = agg.totalLatencyMs / agg.entry.TotalRequests
		}
		result.Entries = append(result.Entries, agg.entry)
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		if result.Entries[i].TotalRequests == result.Entries[j].TotalRequests {
			if result.Entries[i].LastRequestAt.Equal(result.Entries[j].LastRequestAt) {
				if result.Entries[i].Provider == result.Entries[j].Provider {
					if result.Entries[i].App == result.Entries[j].App {
						if result.Entries[i].ClientAPIKey == result.Entries[j].ClientAPIKey {
							return result.Entries[i].Model < result.Entries[j].Model
						}
						return result.Entries[i].ClientAPIKey < result.Entries[j].ClientAPIKey
					}
					return result.Entries[i].App < result.Entries[j].App
				}
				return result.Entries[i].Provider < result.Entries[j].Provider
			}
			return result.Entries[i].LastRequestAt.After(result.Entries[j].LastRequestAt)
		}
		return result.Entries[i].TotalRequests > result.Entries[j].TotalRequests
	})

	return result
}

func applyRollupDetail(rollup *UsageRollupSnapshotV2, detail RequestDetail, tokens TokenStats) {
	if rollup == nil {
		return
	}
	rollup.TotalRequests++
	if detail.Failed {
		rollup.FailureCount++
	} else {
		rollup.SuccessCount++
	}
	rollup.InputTokens += tokens.InputTokens
	rollup.OutputTokens += tokens.OutputTokens
	rollup.ReasoningTokens += tokens.ReasoningTokens
	rollup.CachedTokens += tokens.CachedTokens
	rollup.TotalTokens += tokens.TotalTokens
}

// ApplyUsagePercentCalibrations injects estimated weekly usage percentages into a
// usage snapshot using persisted calibration ratios.
func ApplyUsagePercentCalibrations(snapshot *StatisticsSnapshotV2, cfg internalconfig.UsagePercentCalibrationConfig) {
	if snapshot == nil || len(cfg.Calibrations) == 0 {
		return
	}

	for i := range snapshot.Entries {
		entry := &snapshot.Entries[i]
		calibration := internalconfig.FindBestUsagePercentCalibrationForAuthIndex(cfg.Calibrations, entry.Provider, entry.Model, entry.AuthID, entry.AuthIndex, entry.App)
		if calibration == nil || calibration.TokensPerPercent <= 0 {
			continue
		}
		calibratedValue := float64(entry.TotalTokens)
		switch calibration.TokenKind {
		case "subscription_score":
			calibratedValue = subscriptionUsageScore(entry.InputTokens, entry.OutputTokens, entry.CachedTokens)
		case TokenKindWeightedPriceScore:
			calibratedValue = WeightedPriceScore(entry.InputTokens, entry.OutputTokens, entry.CachedTokens)
		}
		entry.EstimatedWeeklyPercent = calibratedValue / calibration.TokensPerPercent
		entry.CalibratedTokens = int64(calibratedValue)
		entry.TokensPerPercent = calibration.TokensPerPercent
		entry.CalibrationRecordedAt = calibration.RecordedAt

		providerRollup := snapshot.Providers[entry.Provider]
		providerRollup.EstimatedWeeklyPercent += entry.EstimatedWeeklyPercent
		providerRollup.CalibratedTokens += entry.CalibratedTokens
		snapshot.Providers[entry.Provider] = providerRollup

		clientRollup := snapshot.Clients[entry.ClientAPIKey]
		clientRollup.EstimatedWeeklyPercent += entry.EstimatedWeeklyPercent
		clientRollup.CalibratedTokens += entry.CalibratedTokens
		snapshot.Clients[entry.ClientAPIKey] = clientRollup

		appRollup := snapshot.Apps[entry.App]
		appRollup.EstimatedWeeklyPercent += entry.EstimatedWeeklyPercent
		appRollup.CalibratedTokens += entry.CalibratedTokens
		snapshot.Apps[entry.App] = appRollup
	}
}

// TokensForCalibrationScope returns the total tracked tokens for a
// provider/model/auth/app selector using the aggregated V2 entries.
func TokensForCalibrationScope(snapshot StatisticsSnapshotV2, provider string, model string, authID string, app string) int64 {
	return TokensForCalibrationScopeWithAuthIndex(snapshot, provider, model, authID, "", app)
}

// TokensForCalibrationScopeWithAuthIndex returns the total tracked tokens for a
// provider/model/auth/auth-index/app selector using the aggregated V2 entries.
func TokensForCalibrationScopeWithAuthIndex(snapshot StatisticsSnapshotV2, provider string, model string, authID string, authIndex string, app string) int64 {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	app = internalconfig.NormalizeRoutingAppName(app)

	var total int64
	for _, entry := range snapshot.Entries {
		if entry.Provider != provider || strings.TrimSpace(entry.Model) != model {
			continue
		}
		if authID != "" && entry.AuthID != authID {
			continue
		}
		if authIndex != "" && entry.AuthIndex != authIndex {
			continue
		}
		if app != "" && entry.App != app {
			continue
		}
		total += entry.TotalTokens
	}
	return total
}

// SubscriptionUsageScoreForCalibrationScope returns the conservative rolling
// subscription score: output_tokens + 0.15 * fresh_input_tokens.
func SubscriptionUsageScoreForCalibrationScope(snapshot StatisticsSnapshotV2, provider string, model string, authID string, authIndex string, app string) float64 {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	app = internalconfig.NormalizeRoutingAppName(app)

	var total float64
	for _, entry := range snapshot.Entries {
		if entry.Provider != provider || strings.TrimSpace(entry.Model) != model {
			continue
		}
		if authID != "" && entry.AuthID != authID {
			continue
		}
		if authIndex != "" && entry.AuthIndex != authIndex {
			continue
		}
		if app != "" && entry.App != app {
			continue
		}
		total += subscriptionUsageScore(entry.InputTokens, entry.OutputTokens, entry.CachedTokens)
	}
	return total
}

// WeightedPriceScoreForCalibrationScope returns the under-272K price-weighted
// usage score: 5*input_tokens + 30*output_tokens + 0.5*cached_tokens.
func WeightedPriceScoreForCalibrationScope(snapshot StatisticsSnapshotV2, provider string, model string, authID string, authIndex string, app string) float64 {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	authID = strings.TrimSpace(authID)
	authIndex = strings.TrimSpace(authIndex)
	app = internalconfig.NormalizeRoutingAppName(app)

	var total float64
	for _, entry := range snapshot.Entries {
		if entry.Provider != provider || strings.TrimSpace(entry.Model) != model {
			continue
		}
		if authID != "" && entry.AuthID != authID {
			continue
		}
		if authIndex != "" && entry.AuthIndex != authIndex {
			continue
		}
		if app != "" && entry.App != app {
			continue
		}
		total += WeightedPriceScore(entry.InputTokens, entry.OutputTokens, entry.CachedTokens)
	}
	return total
}

func subscriptionUsageScore(inputTokens int64, outputTokens int64, cachedTokens int64) float64 {
	freshInputTokens := inputTokens - cachedTokens
	if freshInputTokens < 0 {
		freshInputTokens = 0
	}
	return float64(outputTokens) + 0.15*float64(freshInputTokens)
}

func WeightedPriceScore(inputTokens int64, outputTokens int64, cachedTokens int64) float64 {
	inputTokens = nonNegativeTokenCount(inputTokens)
	outputTokens = nonNegativeTokenCount(outputTokens)
	cachedTokens = nonNegativeTokenCount(cachedTokens)
	return 5*float64(inputTokens) + 30*float64(outputTokens) + 0.5*float64(cachedTokens)
}

func nonNegativeTokenCount(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
