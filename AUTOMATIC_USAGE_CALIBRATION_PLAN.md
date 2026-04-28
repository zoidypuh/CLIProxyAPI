# Automatic Usage Calibration Plan

## Goal

Add automatic usage-percent calibration on top of the existing manual `usage-percent-calibration` flow. Automatic calibration must only consider enabled file-backed auth entries. Disabled auth files, removed auth files, runtime-only entries without a backing auth file, and API-key-only entries are not calibration candidates.

## Current System

- `internal/api/handlers/management/usage.go` already supports manual calibration:
  - `POST /v0/management/usage/percent-calibration/start`
  - `POST /v0/management/usage/percent-calibration/stop`
  - `GET /v0/management/usage/percent-calibration`
- `internal/config/config.go` persists:
  - one manual active capture
  - saved calibration ratios
  - usage-limit warning settings
- `internal/usage/snapshot_v2.go` applies saved ratios to `/usage/v2`.
- Auth enablement is persisted in auth JSON metadata as `disabled`; runtime auth entries also expose `Auth.Disabled` and `StatusDisabled`.

## Candidate Rule

Create one central helper for automatic calibration eligibility, then use it everywhere automatic calibration lists or starts work:

1. Require a non-empty auth file path from `auth.Attributes["path"]`.
2. Require the backing auth JSON file to exist.
3. Skip when the backing JSON has `"disabled": true`.
4. Skip when the runtime auth has `auth.Disabled == true` or `auth.Status == StatusDisabled`.
5. Skip runtime-only auths that do not point back to a file.
6. Allow virtual/file-derived auths only when their backing file is enabled and the runtime child auth itself is active.

This should live near the automatic calibration code rather than being copied from management UI helpers, because worker code and API code must agree on the same definition.

## UI Flow

Reuse the existing auth-file management UI pattern:

1. Load auth files from the management auth-files API, but show only entries that pass the enabled file-backed eligibility helper.
2. After the user picks an auth file, load models for that auth through the existing auth-file model lookup path.
3. Require the user to choose a model before starting calibration.
4. When calibration starts, store the selected auth id/auth index/model plus the current proxy-recorded token counters and weighted score counters.
5. While active, refresh local proxy counters periodically. Do not ask the user to stop until the selected scope has at least 50,000 newly recorded proxy tokens.
6. Once the 50,000-token border is reached, probe the provider usage percent, compute the weighted score delta, and derive score-per-percent from the provider percent delta.

The UI should never list disabled auth files as calibration targets, even if old usage rows still exist for those auths.

## Implementation Shape

Add a small automatic calibration worker rather than changing the request path. The worker uses organic traffic counters and provider usage probes; it should not send extra model requests by default.

Proposed config:

```yaml
usage-percent-calibration:
  automatic:
    enabled: true
    providers: ["codex"]
    check-interval: "1m"
    sample-border-tokens: 50000
    min-sample-percent: 0.25
    max-captures-per-auth: 1
```

Proposed flow:

1. On startup, start the worker only if `usage-percent-calibration.automatic.enabled` is true and usage statistics are enabled.
2. On each poll, list `authManager.List()` and filter through the enabled file-backed auth helper.
3. For each eligible auth, expose models from the registry-backed auth-file model API. Do not calibrate a model until the user explicitly picks it.
4. When the user starts a capture, store a baseline:
  - provider
  - model
  - auth id
  - auth index
  - token kind
  - start proxy-token counters from `SnapshotV2` or the underlying request details
  - start weighted price score from proxy-recorded request details
  - start 5h/weekly percent from the provider probe
5. On later checks, read the proxy's own token records for the same auth/model scope.
6. If fewer than 50,000 new proxy tokens have been recorded, keep waiting and do not calculate a final ratio.
7. Once the 50,000-token border is reached, probe the provider/account usage percent again. When percent delta is positive, derive weighted-score-per-percent with the same persistence path used by `StopUsagePercentCalibration`.
8. Save the resulting `UsagePercentCalibration`, clear the automatic capture, and let the existing `/usage/v2` and warning code consume the saved calibration.
9. If the auth file becomes disabled, disappears, or the runtime auth becomes disabled while a capture is open, drop that capture without writing a calibration.

## Weighted Price Score

Use proxy-recorded token fields for the calibration delta so the token side and proxy reporting side match:

- `input_tokens`
- `output_tokens`
- `cached_tokens` as cache-read tokens

Do not use provider dashboard token counts for the token delta. Provider data should provide only the start/end usage percentage.

All calibration traffic is expected to stay under the 272,000-token price boundary, so the first implementation should use the normal tier only. Do not add a large-tier branch unless requests above 272,000 become a real target later.

The score can be stored as dollars or as an unscaled price score. The unscaled score is preferable because it avoids tiny floating-point values and the `1_000_000` divisor cancels when calculating score-per-percent.

```text
score = 5*input_tokens + 30*output_tokens + 0.5*cached_tokens
usd = score / 1_000_000
```

If the implementation needs a literal `x*input_tokens + y*output_tokens + z*cache_read` formula:

```text
x=5, y=30, z=0.5
```

The 50,000-token border should still be based on raw proxy-recorded token volume for the selected auth/model scope. The calibration ratio itself should use the weighted price score.

The worker can compute this score from aggregated proxy counters for the selected auth/model scope because the coefficient set is fixed under the normal tier. If large-tier support is added later, switch back to request-detail scoring so the tier can be selected per request before aggregation.

## Provider Probe Interface

Keep provider-specific quota/account calls behind a narrow interface:

```go
type UsagePercentProbe interface {
    Provider() string
    Sample(ctx context.Context, auth *coreauth.Auth) (UsagePercentSample, error)
}

type UsagePercentSample struct {
    FiveHourPercent float64
    WeeklyPercent   float64
    TokenKind       string
    SampledAt       time.Time
}
```

The automatic worker should not know Codex URL details. Codex-specific token refresh, proxy handling, and response parsing should stay in the Codex probe.

## API And UI

Add management endpoints after the worker exists:

- `GET /v0/management/usage/percent-calibration/automatic`
  - returns enabled candidates, active automatic captures, last probe status, and saved calibrations
- `POST /v0/management/usage/percent-calibration/automatic/run`
  - triggers one immediate scan
- `DELETE /v0/management/usage/percent-calibration/automatic`
  - clears automatic captures, not saved manual calibrations

The UI should list only enabled candidates from this endpoint. It should not build automatic candidates directly from raw usage rows, because raw usage can include historical disabled auths.

## Tests

Add focused tests before wiring the worker broadly:

- Eligibility helper skips disabled JSON files.
- Eligibility helper skips runtime disabled auths.
- Eligibility helper includes active file-backed auths.
- Eligibility helper includes active virtual auths only when the backing file is enabled.
- UI candidate endpoint returns only enabled auth files.
- Starting calibration requires both an enabled auth file and a selected model.
- Active calibration remains pending below 50,000 new proxy tokens.
- Weighted price score uses `5*input_tokens + 30*output_tokens + 0.5*cached_tokens`.
- Worker drops an active automatic capture if the auth becomes disabled before completion.
- Worker writes the same ratio fields as manual stop when the second sample is valid.
- Worker does not write a calibration when percent delta is zero or token delta is below threshold.

## Open Point

Full automatic calibration depends on a reliable provider-specific way to read actual 5h/weekly usage percent for the account. If Codex does not expose that number through a stable endpoint, the safe fallback is semi-automatic calibration: the worker can pick enabled auths and track token deltas automatically, but the user still supplies the start/end percentages.
