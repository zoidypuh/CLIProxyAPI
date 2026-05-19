#!/usr/bin/env python3
"""Focused verification for compact cliproxy request-log rendering."""

from __future__ import annotations

from importlib.machinery import SourceFileLoader
from importlib.util import module_from_spec, spec_from_loader
from io import StringIO
import os
from pathlib import Path
import sys
import tempfile
from textwrap import dedent

from rich.console import Console


ROOT = Path(__file__).resolve().parents[1]
TAIL_SCRIPT = ROOT / "scripts" / "cliproxy-tail.sh"


def load_tail_module():
    loader = SourceFileLoader("cliproxy_tail_for_verify", str(TAIL_SCRIPT))
    spec = spec_from_loader(loader.name, loader)
    if spec is None:
        raise RuntimeError("could not create import spec")
    module = module_from_spec(spec)
    sys.modules[loader.name] = module
    loader.exec_module(module)
    return module


def sample_log(
    *,
    prompt: int,
    cached: int,
    output: int,
    auth: str = "qwen-delegate",
    model: str = "mac/qwen3.6-35b-a3b-ud-mlx",
    api_session_id: str = "",
) -> str:
    total = prompt + output
    session_line = f"Session_id: {api_session_id}\n" if api_session_id else ""
    return dedent(
        f"""
        === REQUEST INFO ===
        Timestamp: 2026-05-08T08:36:42Z
        Method: POST
        URL: /v1/chat/completions

        === HEADERS ===
        Authorization: Bearer {auth}
        User-Agent: verify-cliproxy-tail

        === REQUEST BODY ===
        {{"model":"{model}","messages":[{{"role":"user","content":"hi"}}]}}

        === API REQUEST 1 ===
        Upstream URL: http://100.79.30.18:1234/v1/chat/completions
        Auth: provider=lms-mac, auth=local
        {session_line}

        === API RESPONSE 1 ===
        Timestamp: 2026-05-08T08:37:00Z
        Status: 200
        {{"model":"{model}","choices":[{{"finish_reason":"stop"}}],"usage":{{"prompt_tokens":{prompt},"completion_tokens":{output},"total_tokens":{total},"prompt_tokens_details":{{"cached_tokens":{cached}}}}}}}
        """
    ).strip()


def masked_session_log() -> str:
    return sample_log(prompt=569, cached=0, output=167, auth="he...es")


def websocket_error_log() -> str:
    return dedent(
        """
        === REQUEST INFO ===
        Version: dev
        URL: /v1/responses
        Method: GET
        Downstream Transport: websocket
        Upstream Transport: http
        Timestamp: 2026-05-08T09:58:02.245192812Z

        === HEADERS ===
        Authorization: Bearer sk-codex-session
        User-Agent: codex-tui/0.129.0

        === WEBSOCKET TIMELINE ===
        Timestamp: 2026-05-08T09:58:02.348528647Z
        Event: websocket.request
        {"type":"response.create","model":"gpt-5.4-mini","input":"ok"}

        Timestamp: 2026-05-08T09:58:02.398196364Z
        Event: websocket.response
        {"type":"error","status":502,"error":{"message":"unknown provider for model gpt-5.4-mini"}}

        Timestamp: 2026-05-08T09:58:02.398519311Z
        Event: websocket.disconnect
        websocket: close 1006 (abnormal closure): unexpected EOF

        === API ERROR RESPONSE ===
        HTTP Status: 502
        unknown provider for model gpt-5.4-mini

        === API RESPONSE ===
        Timestamp: 2026-05-08T09:58:02.398184389Z
        unknown provider for model gpt-5.4-mini
        """
    ).strip()


def sample_usage_snapshot() -> dict:
    return {
        "usage": {
            "apis": {
                "hermes": {
                    "models": {
                        "codex-hermes": {
                            "details": [
                                {
                                    "timestamp": "2026-05-08T10:50:50Z",
                                    "latency_ms": 9000,
                                    "api_key": "hermes",
                                    "source": "codex-thegismar@gmail.com-pro.json",
                                    "session_id": "session-123",
                                    "tokens": {
                                        "input_tokens": 223380,
                                        "output_tokens": 158,
                                        "reasoning_tokens": 0,
                                        "cached_tokens": 202752,
                                        "total_tokens": 223538,
                                    },
                                    "failed": False,
                                }
                            ]
                        }
                    }
                }
            }
        }
    }


def route_usage_snapshot() -> dict:
    return {
        "usage": {
            "apis": {
                "POST /v1/chat/completions": {
                    "models": {
                        "codex-hermes": {
                            "details": [
                                {
                                    "timestamp": "2026-05-08T10:50:50Z",
                                    "latency_ms": 9000,
                                    "api_key": "POST /v1/chat/completions",
                                    "tokens": {
                                        "input_tokens": 223380,
                                        "output_tokens": 158,
                                        "cached_tokens": 0,
                                        "total_tokens": 223538,
                                    },
                                }
                            ]
                        }
                    }
                }
            }
        }
    }


def codex_usage_snapshot() -> dict:
    return {
        "rate_limit": {
            "primary_window": {
                "used_percent": 4,
                "limit_window_seconds": 18000,
                "reset_at": 1778283371,
            },
            "secondary_window": {
                "used_percent": 22,
                "limit_window_seconds": 604800,
                "reset_at": 1778539703,
            },
        },
        "additional_rate_limits": [
            {
                "limit_name": "GPT-5.3-Codex-Spark",
                "rate_limit": {
                    "primary_window": {
                        "used_percent": 0,
                        "limit_window_seconds": 18000,
                        "reset_at": 1778289429,
                    },
                    "secondary_window": {
                        "used_percent": 0,
                        "limit_window_seconds": 604800,
                        "reset_at": 1778876229,
                    },
                },
            }
        ],
    }


def calibration_usage_snapshot(used_percent: float, reset_at: int) -> dict:
    return {
        "rate_limit": {
            "secondary_window": {
                "used_percent": used_percent,
                "limit_window_seconds": 604800,
                "reset_at": reset_at,
            }
        }
    }


def render_file(module, path: Path) -> str:
    return render_files(module, [path])


def render_files(module, paths: list[Path]) -> str:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None, width=240, highlight=False)
    renderer = module.GroupedRenderer(console)
    for path in paths:
        module.render_file(renderer, path)
    renderer.flush_package()
    renderer.close_group()
    return stream.getvalue()


def render_summaries(module, summaries) -> str:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None, width=240, highlight=False)
    renderer = module.GroupedRenderer(console)
    rendered = set()
    for _key, summary in summaries:
        module.render_summary(renderer, summary, rendered)
    renderer.flush_package()
    renderer.close_group()
    return stream.getvalue()


def main() -> int:
    source = TAIL_SCRIPT.read_text(encoding="utf-8")
    if "HIT" in source:
        raise AssertionError("live request HIT marker is still present in cliproxy tailer")

    module = load_tail_module()

    snapshot, error = module.fetch_management_usage("http://127.0.0.1:1")
    if snapshot is not None or not error:
        raise AssertionError(f"management usage connection failure was not reported: snapshot={snapshot!r} error={error!r}")

    original_urlopen = module.urllib.request.urlopen

    class BadJSONResponse:
        def __enter__(self):
            return self

        def __exit__(self, _exc_type, _exc, _tb):
            return False

        def read(self) -> bytes:
            return b"{not-json"

    try:
        module.urllib.request.urlopen = lambda _req, timeout=0: BadJSONResponse()
        snapshot, error = module.fetch_management_usage("http://127.0.0.1:8317")
        if snapshot is not None or error != "bad-json":
            raise AssertionError(f"management usage bad JSON was not reported cleanly: {snapshot!r} {error!r}")
    finally:
        module.urllib.request.urlopen = original_urlopen

    with tempfile.TemporaryDirectory() as tmp:
        if module.safe_log_dir_entries(Path(tmp) / "missing") != []:
            raise AssertionError("missing log dir should produce an empty polling snapshot")

        missing_active = module.ActiveRequest(Path(tmp) / "rotated-away.log", 0.0)
        module.refresh_active_request(missing_active)
        if missing_active.missing_checks != 1:
            raise AssertionError(f"missing active request was not counted: {missing_active!r}")
        if module.log_has_response(missing_active.path):
            raise AssertionError("missing log file should not be treated as complete")
        status_text = module.live_status_text([], None, "HTTP 503").plain
        if "MANAGEMENT USAGE" not in status_text or "HTTP 503" not in status_text:
            raise AssertionError(f"management usage error was not surfaced in live status: {status_text!r}")

        malformed_path = Path(tmp) / "v1-malformed.log"
        malformed_path.write_bytes(b"\xff\xfe=== REQUEST INFO ===\nTimestamp: not-a-date\n=== RESPONSE ===\nStatus: 200\n{")
        render_file(module, malformed_path)

        recent_dir = Path(tmp) / "recent"
        recent_dir.mkdir()
        older_path = recent_dir / "v1-old.log"
        older_path.write_text(sample_log(prompt=10, cached=0, output=2), encoding="utf-8")
        newer_path = recent_dir / "v1-new.log"
        newer_path.write_text(sample_log(prompt=20, cached=0, output=3), encoding="utf-8")
        os.utime(older_path, (1, 1))
        os.utime(newer_path, (2, 2))
        recent = module.recent_completed_request_logs(recent_dir, 1)
        if recent != [newer_path]:
            raise AssertionError(f"recent completed logs should return newest complete log: {recent!r}")
        if module.recent_completed_request_logs(recent_dir, 0):
            raise AssertionError("recent completed logs should respect limit=0")
        if module.request_log_candidates(recent_dir, 1) != [newer_path]:
            raise AssertionError("request-log candidate scan should be newest-first bounded")
        active_requests = {}
        module.discover_active_requests(
            [older_path, newer_path],
            active_requests,
            rendered_names=set(),
            ignored_names=set(),
            min_mtime_ns=1_500_000_000,
        )
        if sorted(active_requests) != [newer_path.name]:
            raise AssertionError(f"mtime-bounded discovery should only keep new/updated logs: {active_requests!r}")

        log_path = Path(tmp) / "v1-compact.log"
        log_path.write_text(sample_log(prompt=569, cached=0, output=167), encoding="utf-8")
        rendered = render_file(module, log_path)
        lines = [line.rstrip() for line in rendered.splitlines() if line.strip()]

        expected = [
            "05:36:42  mac/qwen3.6-35b-a3b-ud-mlx  session=qwen-delegate",
            "fresh=569  output=167  cached=0  total=736  tokens/s=9.3  duration=18s",
        ]
        if lines != expected:
            raise AssertionError(f"compact lines mismatch:\nexpected={expected!r}\nactual={lines!r}")
        if "qw...te" in rendered or "qwen...gate" in rendered:
            raise AssertionError(f"session/API-key value was shortened:\n{rendered}")

        forbidden = ("POST", "/v1/chat/completions", "status=", "done=", "finish=", "HIT", "◇ USAGE", "╭─", "╰─")
        for needle in forbidden:
            if needle in rendered:
                raise AssertionError(f"forbidden output still present: {needle!r}\n{rendered}")

        second_path = Path(tmp) / "v1-compact-second.log"
        second_path.write_text(sample_log(prompt=42, cached=0, output=8, auth="honcho"), encoding="utf-8")
        two_rendered = render_files(module, [log_path, second_path])
        two_lines = two_rendered.rstrip("\n").splitlines()
        if len(two_lines) != 5 or two_lines[2] != "":
            raise AssertionError(f"requests are not separated by exactly one blank line:\n{two_rendered!r}")
        if two_lines[1] == "" or two_lines[3] == "":
            raise AssertionError(f"blank line was inserted inside a compact two-line entry:\n{two_rendered!r}")
        if "session=honcho" not in two_rendered:
            raise AssertionError(f"second session value was not preserved:\n{two_rendered}")

        masked_path = Path(tmp) / "v1-masked-session.log"
        masked_path.write_text(masked_session_log(), encoding="utf-8")
        masked_rendered = render_file(module, masked_path)
        if "session=hermes" not in masked_rendered or "session=he...es" in masked_rendered:
            raise AssertionError(f"masked local session label was not restored:\n{masked_rendered}")

        named_path = Path(tmp) / "v1-named-copy.log"
        named_path.write_text(
            sample_log(
                prompt=569,
                cached=0,
                output=167,
                auth="hermes",
                model="gpt-5.5",
                api_session_id="session-abc-123",
            ),
            encoding="utf-8",
        )
        render_file(module, named_path)
        named_copies = sorted((Path(tmp) / "by-session").glob("*session-abc-123*.log"))
        if not named_copies:
            raise AssertionError("friendly by-session log copy was not written")
        named_copy = named_copies[-1]
        for needle in ("hermes", "gpt-5.5", "session-abc-123"):
            if needle not in named_copy.name:
                raise AssertionError(f"friendly copy name missing {needle!r}: {named_copy.name}")
        if named_copy.read_text(encoding="utf-8") != named_path.read_text(encoding="utf-8"):
            raise AssertionError("friendly by-session copy did not preserve original log content")

        usage_pairs = module.usage_summaries(sample_usage_snapshot(), Path(tmp))
        if len(usage_pairs) != 1:
            raise AssertionError(f"usage summary count = {len(usage_pairs)}, want 1")
        usage_rendered = render_summaries(module, usage_pairs)
        usage_lines = [line.rstrip() for line in usage_rendered.splitlines() if line.strip()]
        expected_usage = [
            "07:50:50  codex-hermes  session=hermes",
            "fresh=20,628  output=158  cached=202,752  total=223,538  tokens/s=17.6  duration=9.0s",
        ]
        if usage_lines != expected_usage:
            raise AssertionError(f"usage-rendered lines mismatch:\nexpected={expected_usage!r}\nactual={usage_lines!r}")

        route_usage_pairs = module.usage_summaries(route_usage_snapshot(), Path(tmp))
        if route_usage_pairs:
            raise AssertionError(f"route-key management usage should be skipped, got {route_usage_pairs!r}")

        hit_path = Path(tmp) / "v1-cache-hit.log"
        hit_path.write_text(sample_log(prompt=12_000, cached=1_000, output=100), encoding="utf-8")
        hit_summary = module.parse_log(hit_path)
        if hit_summary is None:
            raise AssertionError("cache-hit summary did not parse")
        hit_line = module.GroupedRenderer(Console(file=StringIO())).compact_token_line(
            hit_summary,
            "bright_cyan",
        )
        if str(hit_line.style) == "bold bright_white on red":
            raise AssertionError("cache-hit/mixed line should not be highlighted red")

        miss_path = Path(tmp) / "v1-cache-miss.log"
        miss_path.write_text(sample_log(prompt=12_000, cached=0, output=100), encoding="utf-8")
        miss_summary = module.parse_log(miss_path)
        if miss_summary is None:
            raise AssertionError("cache-miss summary did not parse")
        miss_line = module.GroupedRenderer(Console(file=StringIO())).compact_token_line(
            miss_summary,
            "bright_cyan",
        )
        if str(miss_line.style) == "bold bright_white on red":
            raise AssertionError("non-Hermes cache-miss should not be highlighted red")
        render_file(module, miss_path)
        miss_copy = Path(tmp) / "cache-miss" / "v1-cache-miss.log.txt"
        if miss_copy.exists():
            raise AssertionError(f"non-Hermes cache-miss copy should not be written: {miss_copy}")

        hermes_non_gpt_miss_path = Path(tmp) / "v1-cache-miss-hermes-qwen.log"
        hermes_non_gpt_miss_log = sample_log(prompt=12_000, cached=0, output=100, auth="hermes")
        hermes_non_gpt_miss_path.write_text(hermes_non_gpt_miss_log, encoding="utf-8")
        hermes_non_gpt_miss_summary = module.parse_log(hermes_non_gpt_miss_path)
        if hermes_non_gpt_miss_summary is None:
            raise AssertionError("Hermes non-gpt cache-miss summary did not parse")
        hermes_non_gpt_miss_line = module.GroupedRenderer(Console(file=StringIO())).compact_token_line(
            hermes_non_gpt_miss_summary,
            "bright_cyan",
        )
        if str(hermes_non_gpt_miss_line.style) == "bold bright_white on red":
            raise AssertionError("Hermes non-gpt cache-miss should not be highlighted red")
        render_file(module, hermes_non_gpt_miss_path)
        non_gpt_miss_copy = Path(tmp) / "cache-miss" / "v1-cache-miss-hermes-qwen.log.txt"
        if non_gpt_miss_copy.exists():
            raise AssertionError(f"Hermes non-gpt cache-miss copy should not be written: {non_gpt_miss_copy}")

        hermes_gpt_miss_path = Path(tmp) / "v1-cache-miss-hermes-gpt.log"
        hermes_gpt_miss_log = sample_log(prompt=12_000, cached=0, output=100, auth="hermes", model="gpt-5.5")
        hermes_gpt_miss_path.write_text(hermes_gpt_miss_log, encoding="utf-8")
        hermes_gpt_miss_summary = module.parse_log(hermes_gpt_miss_path)
        if hermes_gpt_miss_summary is None:
            raise AssertionError("Hermes gpt-5.5 cache-miss summary did not parse")
        hermes_gpt_miss_line = module.GroupedRenderer(Console(file=StringIO())).compact_token_line(
            hermes_gpt_miss_summary,
            "bright_cyan",
        )
        if str(hermes_gpt_miss_line.style) != "bold bright_white on red":
            raise AssertionError(f"Hermes gpt-5.5 cache-miss line style = {hermes_gpt_miss_line.style!r}, want red highlight")
        render_file(module, hermes_gpt_miss_path)
        miss_copy = Path(tmp) / "cache-miss" / "v1-cache-miss-hermes-gpt.log.txt"
        if not miss_copy.exists():
            raise AssertionError(f"cache-miss copy was not written: {miss_copy}")
        miss_copy_text = miss_copy.read_text(encoding="utf-8")
        if miss_copy_text != hermes_gpt_miss_log:
            raise AssertionError(
                f"cache-miss copy mismatch:\nexpected={hermes_gpt_miss_log!r}\nactual={miss_copy_text!r}"
            )

        footer = module.footer_usage_text(("chatgpt", codex_usage_snapshot(), None)).plain
        for needle in ("CHATGPT", "78% / 7d"):
            if needle not in footer:
                raise AssertionError(f"Codex footer missing {needle!r}: {footer!r}")
        if "5h" in footer:
            raise AssertionError(f"Codex footer should not include 5h usage: {footer!r}")
        if "Spark" in footer:
            raise AssertionError(f"Codex footer should not include Spark usage: {footer!r}")

        pacer = module.Gpt55UsagePacer()
        pacer.observe_chatgpt_usage(calibration_usage_snapshot(10, 19_000), now=1_000)
        gpt55_path = Path(tmp) / "v1-gpt55-pacer.log"
        gpt55_path.write_text(
            sample_log(prompt=11_000, cached=1_000, output=500, auth="hermes", model="gpt-5.5"),
            encoding="utf-8",
        )
        gpt55_summary = module.parse_log(gpt55_path)
        if gpt55_summary is None:
            raise AssertionError("gpt-5.5 pacer sample did not parse")
        pacer.observe_summary(gpt55_summary)
        pacer.observe_chatgpt_usage(calibration_usage_snapshot(11, 19_000), now=1_600)
        pace = pacer.status_text(now=1_600)
        for needle in ("pace=", "need=", "1%≈", "left≈", "need/h≈"):
            if needle not in pace:
                raise AssertionError(f"GPT-5.5 pacing text missing {needle!r}: {pace!r}")
        paced_footer = module.footer_usage_text(("chatgpt", calibration_usage_snapshot(11, 19_000), None), pacer).plain
        for needle in ("GPT-5.5", "pace=", "1%≈"):
            if needle not in paced_footer:
                raise AssertionError(f"GPT-5.5 paced footer missing {needle!r}: {paced_footer!r}")

        websocket_path = Path(tmp) / "v1-responses-websocket-error.log"
        websocket_path.write_text(websocket_error_log(), encoding="utf-8")
        if not module.log_has_response(websocket_path):
            raise AssertionError("websocket API RESPONSE log was not recognized as complete")
        websocket_rendered = render_file(module, websocket_path)
        websocket_lines = [line.rstrip() for line in websocket_rendered.splitlines() if line.strip()]
        expected_websocket = [
            "06:58:02  gpt-5.4-mini  session=sk-codex-session",
            "fresh=-  output=-  cached=-  total=-  tokens/s=-  duration=0.2s",
        ]
        if websocket_lines != expected_websocket:
            raise AssertionError(
                f"websocket compact lines mismatch:\nexpected={expected_websocket!r}\nactual={websocket_lines!r}"
            )

    print("compact cliproxy log format verification passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
