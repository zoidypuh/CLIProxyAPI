# HANDOFF - cliproxy log tail hardening

## Round 2 - home command UX fix
- Re-opened the issue because the exact home command still looked useless/noisy.
- Confirmed `/home/gismar/log-cliproxy.sh` and `/home/gismar/cliproxy-tail.sh` are Python executables, and `/home/gismar/log-cliproxy` / `/home/gismar/cliproxy-logs.sh` point at `log-cliproxy.sh`.
- Added `/home/gismar/.log-cliproxy.sh -> log-cliproxy.sh` because the reported hidden-script spelling was mentioned too.
- Changed default live behavior: `./log-cliproxy.sh` now renders the latest 5 completed request logs immediately, then watches for new request logs.
- Changed management usage polling and Codex footer usage polling to opt-in defaults (`0`) so the normal command no longer starts with noisy usage-polling banners.
- Kept opt-in flags available:
  - `--management-usage-interval <seconds>`
  - `--footer-usage-interval <seconds>`
  - `--initial <count>`
- Fixed the first startup-render implementation to walk newest-first and stop after enough completed logs, instead of scanning/reading every log before printing.

## Round 3 - restore real-time footer expectation
- Correction: real-time watching was still present after the initial backlog, but the Codex footer usage display had been over-corrected off by default.
- Restored Codex footer usage as a default bottom/live display with `--footer-usage-interval` defaulting to `1800` seconds again.
- Kept `--footer-usage-interval 0` as the opt-out.
- Kept `/v0/management/usage` polling opt-in by default because it can be noisy and can duplicate-looking completed rows with the request-log fallback.
- Normal default behavior is now: show latest completed logs immediately, continue watching new logs in real time, and keep the Codex usage footer enabled.

## Round 4 - log-file watcher recovery
- Correction: the bug was not the display text or the Codex footer; the tool could appear to stop updating.
- Kept `/v0/management/usage` polling opt-in. The default update path is now actual files under `~/.cli-proxy-api/logs`.
- Added bounded newest-first rescans with `--rescan-interval` defaulting to `2s` and `--scan-limit` defaulting to `500`.
- Rescans use file mtimes so old history is not replayed, but newly created or newly updated request logs are picked up even if inotify misses an event or the request log was already open when the tailer started.
- Avoided rereading hundreds of large logs at startup before printing; startup now finds the latest completed entries with a bounded newest-first pass.

## Round 4 Verification
```bash
python3 scripts/verify_cliproxy_tail_format.py
python3 -m py_compile scripts/cliproxy-tail.sh scripts/verify_cliproxy_tail_format.py
```
Result: both exited 0; verifier printed `compact cliproxy log format verification passed`.

Controlled existing-open log test from repo:
```bash
./scripts/cliproxy-tail.sh --initial 0 --footer-usage-interval 0 --management-usage-interval 0 --rescan-interval 0.5 --poll-interval 0.2
```
Setup: created a partial request log directly in `~/.cli-proxy-api/logs`, started the watcher, then appended `API RESPONSE 1`.
Result: watcher showed `ACTIVE 1 request`, then rendered `test/startup-partial` from the log file.

Controlled existing-open log test from home:
```bash
cd /home/gismar && ./log-cliproxy.sh --initial 0 --footer-usage-interval 0 --management-usage-interval 0 --rescan-interval 0.5 --poll-interval 0.2
```
Setup: same partial-log append test in `~/.cli-proxy-api/logs`.
Result: home script showed `ACTIVE 1 request`, then rendered `test/home-partial` from the log file.

## Round 2 Verification
```bash
python3 scripts/verify_cliproxy_tail_format.py
```
Result: `compact cliproxy log format verification passed`

```bash
python3 -m py_compile scripts/cliproxy-tail.sh scripts/verify_cliproxy_tail_format.py
```
Result: exited 0.

Exact home command from `~`:
```bash
cd /home/gismar && timeout 3 ./log-cliproxy.sh
```
Result: printed `Showing latest 5 completed request log(s)`, five compact request summaries, then `Watching /home/gismar/.cli-proxy-api/logs for new request logs. Press Ctrl-C to stop.` No management/footer polling banner appeared. `timeout` exited with status `124`, as expected for a live watcher.

Hidden alias check:
```bash
cd /home/gismar && timeout 3 ./.log-cliproxy.sh --initial 1 --poll-interval 0.1
```
Result: printed the latest completed request summary and entered watch mode.

Direct file check:
```bash
cd /home/gismar && ./log-cliproxy.sh --file /home/gismar/.cli-proxy-api/logs/v1-chat-completions-2026-05-09T072943-19668d36.log
```
Result: rendered the compact request summary.

## Summary
- Investigated the home tailer, repo copy, recent `~/.cli-proxy-api/logs` files, and the tmux pane where the script was run.
- Visible tmux scrollback did not show a Python traceback; it showed `./log-cliproxy.sh` being suspended after repeated Ctrl-C and another tailer instance still running.
- Found real brittle paths in the current script anyway: polling assumed directory entries stayed stat-able, active request files could disappear forever, parse helpers only tolerated `FileNotFoundError`, and management usage polling returned `None` without surfacing why.

## Changed Files
- `scripts/cliproxy-tail.sh`
  - Handles rotating/missing/unreadable request logs with `OSError` guards.
  - Adds `safe_log_dir_entries()` for polling snapshots so deleted files do not crash sorting.
  - Tracks missing active requests and drops them after repeated disappearance instead of keeping stale live state.
  - Makes inotify read/close tolerate file descriptor races.
  - Changes management usage polling to return `(snapshot, error)` and surfaces concise `MANAGEMENT USAGE unavailable=<reason>` status instead of silently swallowing HTTP/JSON/socket failures.
- `scripts/verify_cliproxy_tail_format.py`
  - Adds focused regression coverage for management poll connection failure, bad JSON, missing log dirs, disappeared active files, malformed log bytes, and surfaced management poll errors.
- Home command sync:
  - `/home/gismar/log-cliproxy.sh` synced from repo script.
  - `/home/gismar/cliproxy-tail.sh` restored and synced from repo script.
  - `/home/gismar/log-cliproxy` and `/home/gismar/cliproxy-logs.sh` now point to `log-cliproxy.sh`.

## Verification
```bash
python3 scripts/verify_cliproxy_tail_format.py
```
Result: `compact cliproxy log format verification passed`

```bash
python3 -m py_compile scripts/cliproxy-tail.sh scripts/verify_cliproxy_tail_format.py
```
Result: exited 0.

```bash
timeout 8 ./scripts/cliproxy-tail.sh --management-usage-interval 0.1 --footer-usage-interval 0.1 --poll-interval 0.1
```
Result: ran until `timeout` killed it with status `124`; no traceback.

```bash
/home/gismar/log-cliproxy.sh --file /home/gismar/.cli-proxy-api/logs/v1-chat-completions-2026-05-09T071316-0389d6fa.log
/home/gismar/cliproxy-tail.sh --file /home/gismar/.cli-proxy-api/logs/v1-chat-completions-2026-05-09T071316-0389d6fa.log
```
Result: both rendered the same compact request summary.

## Remaining Risk
- I did not restart `cliproxy`.
- I did not kill the already-running/stopped old tailer processes in tmux; new invocations use the synced fixed scripts.
- No Python traceback was visible in captured tmux scrollback, so the exact prior crash was not confirmed from logs. The fix covers the concrete failure classes present in the script.
