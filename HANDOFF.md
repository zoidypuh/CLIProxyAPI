# HANDOFF - Kanban t_70a7d6a7

## Summary
- Task2: kept the compact request output at exactly two lines per completed request:
  - `<time>  <model-name>  session=<api_key>`
  - `fresh=<fresh_input_tokens>  output=<output_tokens>  cached=<cached_tokens>  total=<total_tokens>  tokens/s=<tokens_per_second>  duration=<duration>`
- Added exactly one blank line between completed request entries, not between the two lines inside an entry.
- Kept session/API-key values raw in this compact log view; examples such as `session=hermes`, `session=honcho`, and `session=qwen-delegate` are not shortened by the tailer.
- Cache-miss summaries now write a compact two-line copy under a `cache-miss/` subfolder beside the source log.
- Cache-miss definition matches the existing red-highlight logic: prompt/input tokens greater than 10,000 and cached tokens below 1,000.
- Follow-up correction: older request logs already contain masked short local session labels such as `he...es` and `ho...ho`; the tailer now restores those known local labels to `hermes` / `honcho` / `qwen-delegate` when rendering.
- Future request logs preserve label-like local bearer values such as `hermes`, `honcho`, `lola`, and `qwen-delegate` while continuing to mask JWTs and obvious secret-key prefixes.
- Follow-up performance correction: live tailing now polls the in-process management usage snapshot at `/v0/management/usage` by default every 1s, bootstraps existing entries as already seen, and renders only new completed usage events from that source. Completed request-log files are still watched as a fallback and deduped against usage-rendered events.
- UsageReporter now falls back to local label-like bearer headers such as `hermes`, `honcho`, `lola`, and `qwen-delegate` when Gin auth middleware did not set an API key. JWTs and obvious secret-key prefixes are ignored by this fallback.
- Preserved the task1 behavior: no live/request `HIT` signal line is rendered by the compact tailer.
- Mirrored the repo script to `/home/gismar/cliproxy-tail.sh`, the real target used by `/home/gismar/cliproxy-logs.sh`.

## Changed Files
- `scripts/cliproxy-tail.sh`
  - Adds blank-line separation between completed request entries while keeping each request to two compact lines.
  - Centralizes compact line generation so cache-miss files contain the same fields the log view intentionally shows.
  - Writes cache-miss compact copies to `cache-miss/<original-log-filename>.txt` beside the source request log.
  - Restores known masked local session labels from older logs before printing `session=...`.
  - Polls `/v0/management/usage` with `--management-usage-interval` / `CLIPROXY_MANAGEMENT_USAGE_INTERVAL` so token-bearing completions can be rendered before the finalized request log file is available. Set the interval to `0` to disable this path.
  - Uses request fingerprints to avoid double-printing when the same completion later arrives through the request-log fallback.
- `scripts/verify_cliproxy_tail_format.py`
  - Verifies no `HIT` marker regressed into the tailer.
  - Verifies compact two-line request output, one blank line between requests, no old verbose fields, full session/API-key values, masked old local session restoration, management-usage rendering, cache-miss red-highlight logic, cache-miss file writing, and the websocket `/v1/responses` completion fixture.
- `internal/runtime/executor/helps/usage_helpers.go`
  - Keeps existing Gin `apiKey` behavior first.
  - Adds a narrow fallback that publishes short local session labels from request headers into usage records when middleware did not populate `apiKey`.
- `internal/runtime/executor/helps/usage_helpers_test.go`
  - Covers Gin `apiKey` precedence, local-label header fallback, and ignoring JWT/secret-like header values.
- `internal/util/provider.go`
  - Preserves label-like local bearer tokens in future request logs while still masking JWTs and obvious secret-key prefixes.
- `internal/util/provider_test.go`
  - Adds coverage for preserving `hermes`, `honcho`, `lola`, and `qwen-delegate`, plus continued masking for JWT/OpenAI-style keys.
- `/home/gismar/cliproxy-tail.sh`
  - Synced from `scripts/cliproxy-tail.sh` so `/home/gismar/cliproxy-logs.sh` uses the new behavior.
- `HANDOFF.md`
  - Records task2 changes, cache-miss file naming/location, and verification while preserving the prior handoff below.

## Cache-Miss Files
- Location: `<source-log-directory>/cache-miss/`
- File name: `<original-log-filename>.txt`
- Content: the same compact two-line request summary printed by the log view, including the now-unshortened `session=<api_key>` value.
- Example: `~/.cli-proxy-api/logs/cache-miss/v1-cache-miss.log.txt`

## Verification
```bash
python3 scripts/verify_cliproxy_tail_format.py
```

Output:
```text
compact cliproxy log format verification passed
```

```bash
python3 -m py_compile scripts/cliproxy-tail.sh scripts/verify_cliproxy_tail_format.py
```

Output: no output; command exited 0.

```bash
go test ./internal/usage
```

Output:
```text
ok  	github.com/router-for-me/CLIProxyAPI/v6/internal/usage	(cached)
```

```bash
go test ./internal/util
```

Output:
```text
ok  	github.com/router-for-me/CLIProxyAPI/v6/internal/util	0.005s
```

```bash
go test ./internal/runtime/executor/helps
```

Output:
```text
ok  	github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps	0.007s
```

```bash
go build -o test-output ./cmd/server && rm test-output
```

Output: no output; command exited 0.

Live current-server usage parser smoke test:
```bash
python3 - <<'PY'
from importlib.machinery import SourceFileLoader
from importlib.util import spec_from_loader, module_from_spec
from pathlib import Path
import sys
path = Path('scripts/cliproxy-tail.sh')
loader = SourceFileLoader('cliproxy_tail_smoke', str(path))
spec = spec_from_loader(loader.name, str(path))
module = module_from_spec(spec)
sys.modules[loader.name] = module
loader.exec_module(module)
snapshot = module.fetch_management_usage('http://127.0.0.1:8317')
pairs = module.usage_summaries(snapshot or {}, Path('/home/gismar/.cli-proxy-api/logs'))
print(f'usage summaries {len(pairs)}')
for _key, summary in pairs[-3:]:
    print('\n'.join(module.compact_lines(summary)))
PY
```

Output excerpt:
```text
usage summaries 2398
08:10:17  gpt-5.5  session=GET /v1/responses
fresh=3,881  output=869  cached=62,848  total=67,598  tokens/s=47.6  duration=18s
```

Note: this live smoke used the already-running server binary, so existing/new in-memory usage rows still show the old aggregate identifier (`GET /v1/responses`) until the server is restarted with the new UsageReporter fallback. The tailer parser path is verified; the backend label fallback is covered by `go test ./internal/runtime/executor/helps`.

```bash
git diff --check
```

Output: no output; command exited 0.

Live old-log smoke test:
```bash
/home/gismar/cliproxy-tail.sh --file /home/gismar/.cli-proxy-api/logs/v1-chat-completions-2026-05-08T104511-45fd6f7d.log | sed -n '1,4p'
```

Output:
```text
07:44:54  mac/qwen3.6-35b-a3b-ud-mlx  session=honcho
fresh=794  output=452  cached=-  total=1,246  tokens/s=26.0  duration=17s
```

## Notes
- Branch: `feat/compact-logs`
- No commit was made.
- Mara/Gismar protocol correction: `[P]` pings are pane-visible messages only; do not use `http://127.0.0.1:8642/v1/responses` or any Hermes/API endpoint for Mara pings.
- Pre-existing dirty files in this worktree remain dirty and are listed in the previous handoff section below.

---

# Previous HANDOFF - Kanban t_94abf8b6

## Summary
- Changed request usage details to expose `api_key` on each request detail.
- Kept the existing Usage Statistics / Request Events visible `session` UI wording, but changed the event row value used by that selector to prefer `api_key`.
- Added backend filtering helpers that compose model alias and API-key filters, covering `codex-hermes` + `lola` / `qwen-delegate` style filtering.
- Old/imported rows without detail-level `api_key` are backfilled from the aggregate API-key bucket so the display and filters do not break.

## Changed Files
- `internal/usage/logger_plugin.go`
  - Adds `RequestDetail.APIKey` JSON field.
  - Stores API key on new request details.
  - Backfills missing detail API key from the aggregate API key during snapshots/imports/filtering.
  - Adds `FilterRequestEvents(snapshot, modelAlias, apiKey)` for combined model-alias + API-key filtering.
- `internal/usage/logger_plugin_test.go`
  - Adds coverage for combined `codex-hermes` + `lola` filtering.
  - Adds coverage for old rows missing detail-level `api_key`, backfilled from `qwen-delegate`.
- `static/management.html`
  - Updates the bundled Request Events flattener to retain the aggregate API-key bucket as `api_key`.
  - Updates the existing `session` row/filter value to prefer `api_key` over old `session_id`.

## Verification
```bash
go test ./internal/usage && go build -o test-output ./cmd/server && rm test-output
```

Output:
```text
ok  	github.com/router-for-me/CLIProxyAPI/v6/internal/usage	0.004s
```

The `go build` command completed successfully and removed `test-output`.

## Caveats
- The UI label still says `session` by design/request; behavior now filters by API key where `api_key` is present/backfilled.
- Existing old in-memory rows with neither detail `api_key` nor aggregate API-key bucket still display safely as `-` and remain selectable under the existing all/blank behavior.
- `cliproxyapi` binary is currently modified in the worktree but was not rebuilt or touched for this task; changes are intentionally left uncommitted.
