package usage

import (
	"context"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatency(t *testing.T) {
	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", details[0].LatencyMs)
	}
}

func TestRequestStatisticsMergeSnapshotDedupIgnoresLatency(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	result := stats.MergeSnapshot(first)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}

	result = stats.MergeSnapshot(second)
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}

func TestFilterRequestEventsCombinesModelAliasAndAPIKey(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "lola",
		Model:       "codex-hermes",
		RequestedAt: timestamp,
		Detail: coreusage.Detail{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "qwen-delegate",
		Model:       "codex-hermes",
		RequestedAt: timestamp.Add(time.Second),
		Detail: coreusage.Detail{
			InputTokens:  20,
			OutputTokens: 10,
			TotalTokens:  30,
		},
	})
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "lola",
		Model:       "gpt-5.4",
		RequestedAt: timestamp.Add(2 * time.Second),
		Detail: coreusage.Detail{
			InputTokens:  7,
			OutputTokens: 8,
			TotalTokens:  15,
		},
	})

	events := FilterRequestEvents(stats.Snapshot(), "codex-hermes", "lola")
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Model != "codex-hermes" {
		t.Fatalf("model = %q, want codex-hermes", events[0].Model)
	}
	if events[0].APIKey != "lola" || events[0].Detail.APIKey != "lola" {
		t.Fatalf("api key = %q detail.api_key = %q, want lola", events[0].APIKey, events[0].Detail.APIKey)
	}
	if events[0].Detail.Tokens.TotalTokens != 150 {
		t.Fatalf("total tokens = %d, want 150", events[0].Detail.Tokens.TotalTokens)
	}
}

func TestFilterRequestEventsBackfillsImportedRowsMissingDetailAPIKey(t *testing.T) {
	snapshot := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"qwen-delegate": {
				Models: map[string]ModelSnapshot{
					"codex-hermes": {
						Details: []RequestDetail{{
							Timestamp: time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC),
							Tokens: TokenStats{
								InputTokens: 10,
								TotalTokens: 10,
							},
						}},
					},
				},
			},
		},
	}

	events := FilterRequestEvents(snapshot, "codex-hermes", "qwen-delegate")
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Detail.APIKey != "qwen-delegate" {
		t.Fatalf("detail.api_key = %q, want qwen-delegate", events[0].Detail.APIKey)
	}
}
