#!/usr/bin/env python3
"""Focused verification for compact cliproxy request-log rendering."""

from __future__ import annotations

from importlib.machinery import SourceFileLoader
from importlib.util import module_from_spec, spec_from_loader
from io import StringIO
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


def sample_log(*, prompt: int, cached: int, output: int, auth: str = "qwen-delegate") -> str:
    total = prompt + output
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
        {{"model":"mac/qwen3.6-35b-a3b-ud-mlx","messages":[{{"role":"user","content":"hi"}}]}}

        === API REQUEST 1 ===
        Upstream URL: http://100.79.30.18:1234/v1/chat/completions
        Auth: provider=lms-mac, auth=local

        === API RESPONSE 1 ===
        Timestamp: 2026-05-08T08:37:00Z
        Status: 200
        {{"model":"mac/qwen3.6-35b-a3b-ud-mlx","choices":[{{"finish_reason":"stop"}}],"usage":{{"prompt_tokens":{prompt},"completion_tokens":{output},"total_tokens":{total},"prompt_tokens_details":{{"cached_tokens":{cached}}}}}}}
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


def render_file(module, path: Path) -> str:
    return render_files(module, [path])


def render_files(module, paths: list[Path]) -> str:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None, width=240, highlight=False)
    renderer = module.GroupedRenderer(console)
    for path in paths:
        module.render_file(renderer, path)
    renderer.close_group()
    return stream.getvalue()


def render_summaries(module, summaries) -> str:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None, width=240, highlight=False)
    renderer = module.GroupedRenderer(console)
    rendered = set()
    for _key, summary in summaries:
        module.render_summary(renderer, summary, rendered)
    renderer.close_group()
    return stream.getvalue()


def main() -> int:
    source = TAIL_SCRIPT.read_text(encoding="utf-8")
    if "HIT" in source:
        raise AssertionError("live request HIT marker is still present in cliproxy tailer")

    module = load_tail_module()
    with tempfile.TemporaryDirectory() as tmp:
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

        hermes_miss_path = Path(tmp) / "v1-cache-miss-hermes.log"
        hermes_miss_log = sample_log(prompt=12_000, cached=0, output=100, auth="hermes")
        hermes_miss_path.write_text(hermes_miss_log, encoding="utf-8")
        hermes_miss_summary = module.parse_log(hermes_miss_path)
        if hermes_miss_summary is None:
            raise AssertionError("Hermes cache-miss summary did not parse")
        hermes_miss_line = module.GroupedRenderer(Console(file=StringIO())).compact_token_line(
            hermes_miss_summary,
            "bright_cyan",
        )
        if str(hermes_miss_line.style) != "bold bright_white on red":
            raise AssertionError(f"Hermes cache-miss line style = {hermes_miss_line.style!r}, want red highlight")
        render_file(module, hermes_miss_path)
        miss_copy = Path(tmp) / "cache-miss" / "v1-cache-miss-hermes.log.txt"
        if not miss_copy.exists():
            raise AssertionError(f"cache-miss copy was not written: {miss_copy}")
        miss_copy_text = miss_copy.read_text(encoding="utf-8")
        if miss_copy_text != hermes_miss_log:
            raise AssertionError(
                f"cache-miss copy mismatch:\nexpected={hermes_miss_log!r}\nactual={miss_copy_text!r}"
            )

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
