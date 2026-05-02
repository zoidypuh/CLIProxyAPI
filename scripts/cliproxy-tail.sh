#!/usr/bin/env python3
"""Live-tail CLIProxyAPI request logs with Rich grouped output."""

from __future__ import annotations

import argparse
import base64
import colorsys
import ctypes
import ctypes.util
from dataclasses import dataclass
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import select
import struct
import sys
import time
from typing import Any
import urllib.error
import urllib.request
from urllib.parse import urlparse

try:
    from rich.console import Console
    from rich.text import Text
except ModuleNotFoundError:
    print("ERROR: rich is required. Install it with: python3 -m pip install --user rich", file=sys.stderr)
    raise SystemExit(1)


SECTION_RE = re.compile(r"^=== ([A-Z0-9 ]+) ===\s*$", re.M)
REQUEST_LOG_RE = re.compile(r"^(v1|claude|gemini|codex|openai|anthropic)-")
CHATGPT_USAGE_URL = "https://chatgpt.com/backend-api/wham/usage"
ANTHROPIC_USAGE_URL = "https://api.anthropic.com/api/oauth/usage"


@dataclass
class TokenStats:
    prompt: int | None = None
    cached: int | None = None
    output: int | None = None
    reasoning: int | None = None
    total: int | None = None


@dataclass
class RequestSummary:
    path: Path
    stamp: str
    model: str
    client: str
    method: str
    endpoint: str
    message_count: int
    status: str
    duration: float | None
    finish: str
    upstream: str
    provider: str
    tokens: TokenStats
    tokens_per_second: float | None

    @property
    def group_key(self) -> tuple[str, str]:
        return (self.model or "unknown", self.client or "unknown")


def env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None:
        return default
    try:
        return int(raw)
    except ValueError:
        return default


def default_base_url() -> str:
    return os.environ.get("CLIPROXY_BASE_URL") or f"http://127.0.0.1:{os.environ.get('CLIPROXY_PORT', '8317')}"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Live-tail CLIProxyAPI request logs with grouped Rich output.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument(
        "--log-dir",
        default=os.environ.get("CLIPROXY_LOG_DIR", str(Path.home() / ".cli-proxy-api" / "logs")),
        help="Directory containing CLIProxyAPI request logs.",
    )
    parser.add_argument(
        "--base-url",
        default=default_base_url(),
        help="CLIProxyAPI base URL. Kept for compatibility; usage snapshots read auth files.",
    )
    parser.add_argument(
        "--usage-interval",
        type=int,
        default=env_int("CLIPROXY_USAGE_INTERVAL", 300),
        help="Seconds between upstream usage snapshots. Use 0 to disable.",
    )
    parser.add_argument(
        "--auth-dir",
        default=os.environ.get("CLIPROXY_AUTH_DIR", str(Path.home() / ".cli-proxy-api")),
        help="Directory containing CLIProxyAPI auth files for usage snapshots.",
    )
    parser.add_argument(
        "--codex-auth-file",
        default=os.environ.get("CLIPROXY_CODEX_AUTH_FILE", ""),
        help="Specific Codex auth JSON file for ChatGPT usage snapshots.",
    )
    parser.add_argument(
        "--claude-auth-file",
        default=os.environ.get("CLIPROXY_CLAUDE_AUTH_FILE", ""),
        help="Specific Claude auth JSON file for Anthropic usage snapshots.",
    )
    parser.add_argument(
        "--poll-interval",
        type=float,
        default=0.5,
        help="Polling interval used when Linux inotify is unavailable.",
    )
    parser.add_argument(
        "--file",
        action="append",
        default=[],
        help="Render an existing log file and exit. May be repeated.",
    )
    return parser.parse_args()


def is_request_log(name: str) -> bool:
    return bool(REQUEST_LOG_RE.match(name))


def section(text: str, name: str, *, last: bool = False) -> str:
    matches = [m for m in SECTION_RE.finditer(text) if m.group(1).strip() == name]
    if not matches:
        return ""
    match = matches[-1] if last else matches[0]
    start = match.end()
    next_match = SECTION_RE.search(text, start)
    end = next_match.start() if next_match else len(text)
    return text[start:end].strip()


def first_json(blob: str) -> dict[str, Any] | list[Any] | None:
    blob = blob.strip()
    if not blob:
        return None

    lines = blob.splitlines()
    while lines and not lines[0].lstrip().startswith(("{", "[", "data:", "event:")):
        lines.pop(0)
    blob = "\n".join(lines).strip()

    if blob.startswith("data:") or "\ndata:" in blob or blob.startswith("event:"):
        return parse_sse_json(blob)

    try:
        return json.loads(blob)
    except Exception:
        pass

    depth = 0
    end = -1
    for idx, char in enumerate(blob):
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                end = idx + 1
    if end > 0:
        try:
            return json.loads(blob[:end])
        except Exception:
            return None
    return None


def parse_sse_json(blob: str) -> dict[str, Any] | None:
    merged: dict[str, Any] = {
        "usage": {},
        "usageMetadata": {},
        "choices": [{}],
        "model": None,
        "stop_reason": None,
        "created_at": None,
        "completed_at": None,
    }

    for line in blob.splitlines():
        line = line.strip()
        if not line.startswith("data:"):
            continue
        payload = line[5:].strip()
        if not payload or payload == "[DONE]":
            continue
        try:
            obj = json.loads(payload)
        except Exception:
            continue
        if not isinstance(obj, dict):
            continue

        if obj.get("model"):
            merged["model"] = obj["model"]

        response_obj = obj.get("response")
        if isinstance(response_obj, dict):
            if response_obj.get("model"):
                merged["model"] = response_obj["model"]
            if response_obj.get("created_at") is not None:
                merged["created_at"] = response_obj["created_at"]
            if response_obj.get("completed_at") is not None:
                merged["completed_at"] = response_obj["completed_at"]

        choices = obj.get("choices") or []
        if choices and isinstance(choices, list):
            finish = choices[0].get("finish_reason")
            if finish:
                merged["choices"][0]["finish_reason"] = finish

        if obj.get("stop_reason"):
            merged["stop_reason"] = obj["stop_reason"]

        delta = obj.get("delta")
        if isinstance(delta, dict):
            if delta.get("usage"):
                usage = delta.get("usage")
                if isinstance(usage, dict):
                    merged["usage"].update(usage)
            if delta.get("stop_reason"):
                merged["stop_reason"] = delta["stop_reason"]

        message = obj.get("message")
        if isinstance(message, dict) and isinstance(message.get("usage"), dict):
            merged["usage"].update(message["usage"])

        usage = obj.get("usage")
        if not usage and isinstance(response_obj, dict):
            usage = response_obj.get("usage")
        if isinstance(usage, dict):
            merged["usage"].update(usage)

        usage_metadata = obj.get("usageMetadata") or obj.get("usage_metadata")
        if not usage_metadata and isinstance(response_obj, dict):
            usage_metadata = response_obj.get("usageMetadata") or response_obj.get("usage_metadata")
        if isinstance(usage_metadata, dict):
            merged["usageMetadata"].update(usage_metadata)

    if (
        merged["usage"]
        or merged["usageMetadata"]
        or merged["model"]
        or merged["choices"][0].get("finish_reason")
    ):
        return merged
    return None


def parse_headers(blob: str) -> dict[str, str]:
    headers: dict[str, str] = {}
    for raw in blob.splitlines():
        if ":" not in raw:
            continue
        key, value = raw.split(":", 1)
        key = key.strip().lower()
        value = value.strip()
        if key:
            headers[key] = value
    return headers


def compact_user_agent(value: str) -> str:
    value = re.sub(r"\s+", " ", (value or "").strip())
    if len(value) <= 52:
        return value
    return value[:49] + "..."


def client_key(headers: dict[str, str]) -> str:
    raw = headers.get("authorization") or headers.get("x-api-key") or headers.get("api-key") or ""
    raw = raw.strip()
    if raw.lower().startswith("bearer "):
        raw = raw[7:].strip()
    return raw


def client_label_from_key(key: str) -> str:
    key = re.sub(r"\s+", "", key or "")
    if not key:
        return ""
    key_lower = key.lower()
    if key_lower in {"codex", "co...ex"} or key_lower.endswith("odex"):
        return "Codex CLI"
    if key_lower in {"hermes", "he...es"} or key_lower.endswith("rmes"):
        return "Hermes Agent"
    if len(key) <= 6 or "..." in key:
        return key
    return ""


def client_label(headers: dict[str, str]) -> str:
    from_key = client_label_from_key(client_key(headers))
    if from_key:
        return from_key
    return compact_user_agent(headers.get("user-agent", ""))


def normalize_iso_timestamp(value: str) -> str:
    value = value.strip()
    value = re.sub(r"(\.\d{6})\d+([+-]\d\d:?\d\d|Z)?$", r"\1\2", value)
    if value.endswith("Z"):
        value = value[:-1] + "+00:00"
    return value


def parse_datetime(value: str) -> datetime | None:
    try:
        return datetime.fromisoformat(normalize_iso_timestamp(value))
    except Exception:
        return None


def response_duration_seconds(obj: Any) -> float | None:
    if not isinstance(obj, dict):
        return None
    created = obj.get("created_at") or obj.get("created")
    completed = obj.get("completed_at") or obj.get("completed")
    if created is None or completed is None:
        return None
    try:
        seconds = float(completed) - float(created)
    except Exception:
        return None
    return seconds if seconds > 0 else None


def as_int(value: Any) -> int | None:
    if value is None:
        return None
    try:
        return int(value)
    except Exception:
        return None


def as_float(value: Any) -> float | None:
    if value is None:
        return None
    try:
        return float(value)
    except Exception:
        return None


def first_existing(mapping: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in mapping:
            return mapping.get(key)
    return None


def parse_token_stats(obj: Any) -> TokenStats:
    if not isinstance(obj, dict):
        return TokenStats()

    usage = obj.get("usage") if isinstance(obj.get("usage"), dict) else {}
    usage_metadata = first_existing(obj, "usageMetadata", "usage_metadata")
    if not isinstance(usage_metadata, dict):
        usage_metadata = {}

    tokens = TokenStats()

    if "prompt_tokens" in usage or "completion_tokens" in usage:
        tokens.prompt = as_int(usage.get("prompt_tokens"))
        tokens.output = as_int(usage.get("completion_tokens"))
        tokens.total = as_int(usage.get("total_tokens"))
        prompt_details = usage.get("prompt_tokens_details") or {}
        completion_details = usage.get("completion_tokens_details") or {}
        if isinstance(prompt_details, dict):
            tokens.cached = as_int(prompt_details.get("cached_tokens"))
        if isinstance(completion_details, dict):
            tokens.reasoning = as_int(completion_details.get("reasoning_tokens"))
    elif "input_tokens" in usage or "output_tokens" in usage:
        input_tokens = as_int(usage.get("input_tokens"))
        cache_read = as_int(usage.get("cache_read_input_tokens")) or 0
        cache_create = as_int(usage.get("cache_creation_input_tokens")) or 0
        tokens.prompt = input_tokens
        input_details = usage.get("input_tokens_details") or {}
        if isinstance(input_details, dict):
            tokens.cached = as_int(input_details.get("cached_tokens"))
        if cache_read or cache_create:
            tokens.prompt = (input_tokens or 0) + cache_read + cache_create
            tokens.cached = cache_read or tokens.cached
        tokens.output = as_int(usage.get("output_tokens"))
        tokens.total = as_int(usage.get("total_tokens"))
        output_details = usage.get("output_tokens_details") or {}
        if isinstance(output_details, dict):
            tokens.reasoning = as_int(output_details.get("reasoning_tokens"))
    elif usage_metadata:
        tokens.prompt = as_int(
            first_existing(usage_metadata, "promptTokenCount", "prompt_token_count", "inputTokenCount")
        )
        tokens.output = as_int(
            first_existing(usage_metadata, "candidatesTokenCount", "candidates_token_count", "outputTokenCount")
        )
        tokens.total = as_int(first_existing(usage_metadata, "totalTokenCount", "total_token_count"))
        tokens.cached = as_int(
            first_existing(usage_metadata, "cachedContentTokenCount", "cached_content_token_count")
        )
        tokens.reasoning = as_int(
            first_existing(usage_metadata, "thoughtsTokenCount", "thoughts_token_count")
        )

    if tokens.total is None and tokens.prompt is not None and tokens.output is not None:
        tokens.total = tokens.prompt + tokens.output
    return tokens


def has_tokens(tokens: TokenStats) -> bool:
    return any(
        value is not None
        for value in (tokens.prompt, tokens.cached, tokens.output, tokens.reasoning, tokens.total)
    )


def parse_finish(resp: Any) -> str:
    if not isinstance(resp, dict):
        return ""
    choices = resp.get("choices") or []
    if choices and isinstance(choices, list) and isinstance(choices[0], dict):
        finish = choices[0].get("finish_reason") or choices[0].get("native_finish_reason")
        if finish:
            return str(finish)
    return str(resp.get("stop_reason") or "")


def parse_log(path: Path) -> RequestSummary | None:
    try:
        text = path.read_text(errors="replace")
    except FileNotFoundError:
        return None

    info = section(text, "REQUEST INFO")
    ts_match = re.search(r"Timestamp:\s*([0-9T:\-\.\+Z]+)", info)
    start_dt = parse_datetime(ts_match.group(1)) if ts_match else None
    if start_dt is None:
        start_dt = datetime.now()
    stamp = start_dt.strftime("%H:%M:%S")

    method_match = re.search(r"Method:\s*(\S+)", info)
    method = method_match.group(1) if method_match else ""

    url_match = re.search(r"URL:\s*(\S+)", info)
    endpoint = url_match.group(1) if url_match else path.name

    request_json = first_json(section(text, "REQUEST BODY"))
    headers = parse_headers(section(text, "HEADERS"))
    response_text = section(text, "RESPONSE", last=True)
    api_response_text = section(text, "API RESPONSE 1", last=True)
    response_json = first_json(response_text)
    api_response_json = first_json(api_response_text)
    parsed_response = response_json if response_json is not None else api_response_json

    status_match = re.search(r"\bStatus:\s*(\d+)", response_text or api_response_text)
    status = status_match.group(1) if status_match else ""

    api_request = section(text, "API REQUEST 1")
    upstream_match = re.search(r"Upstream URL:\s*(\S+)", api_request)
    upstream_url = upstream_match.group(1) if upstream_match else ""
    upstream = urlparse(upstream_url).netloc if upstream_url else ""

    auth_match = re.search(r"Auth:\s*([^\n]+)", api_request)
    auth = auth_match.group(1).strip() if auth_match else ""
    provider_match = re.search(r"provider=([^,\s]+)", auth)
    provider = provider_match.group(1) if provider_match else ""

    resp_ts_match = re.search(r"Timestamp:\s*([0-9T:\-\.\+Z]+)", api_response_text or response_text)
    duration = None
    if resp_ts_match:
        end_dt = parse_datetime(resp_ts_match.group(1))
        if end_dt is not None:
            duration = max(0.0, (end_dt - start_dt).total_seconds())

    generation_duration = response_duration_seconds(api_response_json) or response_duration_seconds(response_json)
    if generation_duration is not None and (duration is None or generation_duration > duration):
        duration = generation_duration

    request_dict = request_json if isinstance(request_json, dict) else {}
    response_dict = parsed_response if isinstance(parsed_response, dict) else {}
    api_response_dict = api_response_json if isinstance(api_response_json, dict) else {}

    model = (
        request_dict.get("model")
        or response_dict.get("model")
        or api_response_dict.get("model")
        or "unknown"
    )
    messages = request_dict.get("messages") or []
    message_count = len(messages) if isinstance(messages, list) else 0

    tokens = parse_token_stats(response_dict)
    if not has_tokens(tokens):
        tokens = parse_token_stats(api_response_dict)

    output_tokens = tokens.output
    tokens_per_second = None
    if output_tokens is not None and duration and duration > 0:
        tokens_per_second = output_tokens / duration

    return RequestSummary(
        path=path,
        stamp=stamp,
        model=str(model),
        client=client_label(headers),
        method=method,
        endpoint=endpoint,
        message_count=message_count,
        status=status,
        duration=duration,
        finish=parse_finish(response_dict) or parse_finish(api_response_dict),
        upstream=upstream,
        provider=provider,
        tokens=tokens,
        tokens_per_second=tokens_per_second,
    )


def fmt_num(value: int | None) -> str:
    return "-" if value is None else f"{value:,}"


def fmt_duration(value: float | None) -> str:
    if value is None:
        return "-"
    if value < 10:
        return f"{value:.1f}s"
    return f"{value:.0f}s"


def fmt_rate(value: float | None) -> str:
    if value is None:
        return "-"
    if value >= 100:
        return f"{value:.0f}"
    return f"{value:.1f}"


def stable_style(name: str) -> str:
    digest = hashlib.sha1(name.encode("utf-8", "replace")).digest()
    hue = int.from_bytes(digest[:2], "big") / 65535
    saturation = 0.68 + (digest[2] / 255) * 0.18
    lightness = 0.58 + (digest[3] / 255) * 0.16
    red, green, blue = colorsys.hls_to_rgb(hue, lightness, saturation)
    return f"#{int(red * 255):02x}{int(green * 255):02x}{int(blue * 255):02x}"


def status_style(status: str) -> str:
    try:
        code = int(status)
    except ValueError:
        return "bright_black"
    if 200 <= code < 300:
        return "bold green"
    if 300 <= code < 400:
        return "bold yellow"
    return "bold red"


def append_kv(text: Text, label: str, value: Any, value_style: str, *, pad: bool = True) -> None:
    if value is None or value == "":
        return
    if pad and text.plain and not text.plain.endswith((" ", "─")):
        text.append("  ")
    text.append(f"{label}=", style="dim")
    text.append(str(value), style=value_style)


def fresh_tokens(tokens: TokenStats) -> int | None:
    if tokens.prompt is None:
        return None
    if not tokens.cached:
        return tokens.prompt
    return max(0, tokens.prompt - tokens.cached)


def clamp_percent(value: float) -> float:
    return max(0.0, min(100.0, value))


def fmt_percent(value: float) -> str:
    if abs(value - round(value)) < 0.05:
        return f"{round(value):.0f}%"
    return f"{value:.1f}%"


def usage_window_label(seconds: int | None) -> str:
    if not seconds or seconds <= 0:
        return "window"
    if seconds % 86400 == 0:
        return f"{seconds // 86400}d"
    if seconds % 3600 == 0:
        return f"{seconds // 3600}h"
    hours = seconds / 3600
    if hours >= 1:
        return f"{hours:.1f}h"
    return f"{seconds}s"


def format_reset_time(value: Any) -> str:
    if value is None or value == "":
        return ""

    numeric = as_float(value)
    if numeric is not None:
        if numeric > 1_000_000_000_000:
            numeric = numeric / 1000
        try:
            return datetime.fromtimestamp(numeric).strftime("%m-%d %H:%M")
        except Exception:
            return ""

    if isinstance(value, str):
        parsed = parse_datetime(value)
        if parsed is not None:
            return parsed.astimezone().strftime("%m-%d %H:%M")

    return ""


def fallback_reset_time(seconds: int | float | None) -> str:
    if seconds is None or seconds <= 0:
        return ""
    try:
        return datetime.fromtimestamp(time.time() + float(seconds)).strftime("%m-%d %H:%M")
    except Exception:
        return ""


def usage_reset_label(window: dict[str, Any], fallback_seconds: int | None = None) -> str:
    reset_at = first_existing(window, "reset_at", "resets_at", "resetAt", "resetsAt")
    reset_label = format_reset_time(reset_at)
    if reset_label:
        return reset_label

    reset_after = as_float(first_existing(window, "reset_after_seconds", "resetAfterSeconds"))
    if reset_after is not None:
        return fallback_reset_time(reset_after)

    return fallback_reset_time(fallback_seconds)


def with_reset_label(part: str, reset_label: str) -> str:
    if reset_label:
        return f"{part} ends {reset_label}"
    return part


def chatgpt_usage_left_part(window: Any) -> str | None:
    if not isinstance(window, dict):
        return None
    used_percent = as_float(window.get("used_percent"))
    if used_percent is None:
        return None
    left_percent = clamp_percent(100.0 - used_percent)
    window_seconds = as_int(window.get("limit_window_seconds"))
    label = usage_window_label(window_seconds)
    part = f"{fmt_percent(left_percent)} / {label}"
    return with_reset_label(part, usage_reset_label(window, window_seconds))


def chatgpt_usage_left_parts(usage: dict[str, Any]) -> list[str]:
    rate_limit = usage.get("rate_limit")
    if not isinstance(rate_limit, dict):
        return []
    parts = [
        chatgpt_usage_left_part(rate_limit.get("primary_window")),
        chatgpt_usage_left_part(rate_limit.get("secondary_window")),
    ]
    return [part for part in parts if part]


def anthropic_usage_left_part(window: Any, label: str) -> str | None:
    if not isinstance(window, dict):
        return None
    used_percent = as_float(window.get("utilization"))
    if used_percent is None:
        return None
    left_percent = clamp_percent(100.0 - used_percent)
    fallback_seconds = {"5h": 5 * 3600, "7d": 7 * 86400}.get(label)
    part = f"{fmt_percent(left_percent)} / {label}"
    return with_reset_label(part, usage_reset_label(window, fallback_seconds))


def anthropic_usage_left_parts(usage: dict[str, Any]) -> list[str]:
    parts = [
        anthropic_usage_left_part(usage.get("five_hour"), "5h"),
        anthropic_usage_left_part(usage.get("seven_day"), "7d"),
    ]
    return [part for part in parts if part]


def usage_left_parts(upstream: str, usage: dict[str, Any]) -> list[str]:
    if upstream == "anthropic":
        return anthropic_usage_left_parts(usage)
    return chatgpt_usage_left_parts(usage)


def decode_jwt_payload(token: str) -> dict[str, Any]:
    parts = token.split(".")
    if len(parts) < 2:
        return {}
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    try:
        decoded = base64.urlsafe_b64decode(payload.encode("ascii"))
        obj = json.loads(decoded.decode("utf-8", "replace"))
    except Exception:
        return {}
    return obj if isinstance(obj, dict) else {}


def id_token_account_id(raw: Any) -> str:
    obj: dict[str, Any]
    if isinstance(raw, dict):
        obj = raw
    elif isinstance(raw, str):
        obj = decode_jwt_payload(raw)
    else:
        return ""

    auth_claim = obj.get("https://api.openai.com/auth")
    if isinstance(auth_claim, dict):
        account_id = auth_claim.get("chatgpt_account_id")
        if account_id:
            return str(account_id)
    account_id = obj.get("chatgpt_account_id")
    return str(account_id) if account_id else ""


def auth_priority(auth: dict[str, Any]) -> int:
    value = auth.get("priority")
    try:
        return int(value)
    except Exception:
        return 0


def codex_auth_candidates(auth_dir: str, auth_file: str) -> list[Path]:
    if auth_file:
        return [Path(auth_file).expanduser()]
    root = Path(auth_dir).expanduser()
    if not root.is_dir():
        return []
    return sorted(root.glob("codex-*.json"))


def claude_auth_candidates(auth_dir: str, auth_file: str) -> list[Path]:
    if auth_file:
        return [Path(auth_file).expanduser()]
    root = Path(auth_dir).expanduser()
    if not root.is_dir():
        return []
    return sorted(root.glob("claude-*.json"))


def load_chatgpt_auth(auth_dir: str, auth_file: str) -> tuple[str, str, str | None]:
    candidates: list[tuple[int, str, str]] = []
    saw_codex = False
    for path in codex_auth_candidates(auth_dir, auth_file):
        try:
            obj = json.loads(path.read_text(errors="replace"))
        except Exception:
            continue
        if not isinstance(obj, dict):
            continue
        if obj.get("type") != "codex" and not path.name.startswith("codex-"):
            continue
        saw_codex = True
        if obj.get("disabled") is True or obj.get("expired") is True:
            continue
        token = obj.get("access_token")
        if not isinstance(token, str) or not token.strip():
            continue
        account_id = obj.get("account_id")
        if not account_id:
            account_id = id_token_account_id(obj.get("id_token"))
        candidates.append((auth_priority(obj), token.strip(), str(account_id or "")))

    if not candidates:
        return "", "", "no-access-token" if saw_codex else "no-codex-auth"
    candidates.sort(key=lambda item: item[0], reverse=True)
    _, token, account_id = candidates[0]
    return token, account_id, None


def load_anthropic_auth(auth_dir: str, auth_file: str) -> tuple[str, str | None]:
    saw_claude = False
    for path in claude_auth_candidates(auth_dir, auth_file):
        try:
            obj = json.loads(path.read_text(errors="replace"))
        except Exception:
            continue
        if not isinstance(obj, dict):
            continue
        if obj.get("type") != "claude" and not path.name.startswith("claude-"):
            continue
        saw_claude = True
        if obj.get("disabled") is True or obj.get("expired") is True:
            continue
        token = obj.get("access_token")
        if isinstance(token, str) and token.strip():
            return token.strip(), None
    return "", "no-access-token" if saw_claude else "no-claude-auth"


class GroupedRenderer:
    def __init__(self, console: Console) -> None:
        self.console = console
        self.current_key: tuple[str, str] | None = None
        self.current_style = ""
        self.open_group = False

    def close_group(self) -> None:
        if self.open_group:
            self.console.print(Text("╰─", style=self.current_style), soft_wrap=True)
            self.open_group = False

    def separate_from_request_group(self, *, blank: bool = True) -> None:
        if self.current_key is None:
            return
        self.close_group()
        if blank:
            self.console.print()
        self.current_key = None
        self.current_style = ""

    def render_request(self, summary: RequestSummary) -> None:
        key = summary.group_key
        style = stable_style("\0".join(key))
        if key != self.current_key:
            if self.current_key is not None:
                self.close_group()
                self.console.print()
            self.current_key = key
            self.current_style = style
            self.open_group = True
            self.console.print(self.group_header(summary, style), soft_wrap=True)

        self.console.print(self.request_line(summary, style), soft_wrap=True)
        self.console.print(self.route_line(summary, style), soft_wrap=True)
        token_line = self.token_line(summary, style)
        if token_line is not None:
            self.console.print(token_line, soft_wrap=True)

    def group_header(self, summary: RequestSummary, style: str) -> Text:
        text = Text("╭─ ", style=style)
        text.append(summary.model or "unknown", style=f"bold {style}")
        text.append("  ")
        append_kv(text, "client", summary.client or "unknown", style)
        return text

    def request_line(self, summary: RequestSummary, style: str) -> Text:
        text = Text("├─ ", style=style)
        text.append(summary.stamp, style=f"bold {style}")
        append_kv(text, "status", summary.status, status_style(summary.status))
        append_kv(text, "duration", fmt_duration(summary.duration), f"bold {style}")
        append_kv(text, "finish", summary.finish, style)
        return text

    def route_line(self, summary: RequestSummary, style: str) -> Text:
        text = Text("│  ", style=style)
        if summary.method:
            text.append(summary.method, style=f"bold {style}")
            text.append("  ")
        text.append(summary.endpoint, style=style)
        append_kv(text, "messages", summary.message_count if summary.message_count else None, style)
        append_kv(text, "provider", summary.provider, style)
        append_kv(text, "upstream", summary.upstream, style)
        return text

    def token_line(self, summary: RequestSummary, style: str) -> Text | None:
        tokens = summary.tokens
        if not has_tokens(tokens):
            return None
        text = Text("│  tokens  ", style=style)
        append_kv(text, "fresh", fmt_num(fresh_tokens(tokens)), f"bold {style}", pad=False)
        append_kv(text, "cached", fmt_num(tokens.cached) if tokens.cached else None, style)
        append_kv(text, "output", fmt_num(tokens.output), f"bold {style}")
        append_kv(text, "reasoning", fmt_num(tokens.reasoning) if tokens.reasoning else None, style)
        append_kv(text, "total", fmt_num(tokens.total), f"bold {style}")
        append_kv(text, "tokens/s", fmt_rate(summary.tokens_per_second), f"bold {style}")
        return text

    def usage_line(self, upstream: str, usage: dict[str, Any] | None, error: str | None = None) -> Text:
        text = Text("◇ USAGE", style="bold bright_white")
        text.append("  ")
        text.append(upstream.upper(), style="bold bright_cyan")
        if error:
            text.append("  unavailable=", style="dim")
            text.append(error, style="red")
            return text
        if not usage:
            text.append("  unavailable", style="red")
            return text

        parts = usage_left_parts(upstream, usage)
        if not parts:
            text.append("  unavailable=missing-windows", style="red")
            return text
        text.append("  left ", style="dim")
        text.append(", ".join(parts), style="bold bright_yellow")
        return text

    def render_usage(self, upstream: str, usage: dict[str, Any] | None, error: str | None = None) -> None:
        self.render_usage_block([(upstream, usage, error)])

    def render_usage_block(self, snapshots: list[tuple[str, dict[str, Any] | None, str | None]]) -> None:
        self.separate_from_request_group(blank=False)
        self.console.print()
        self.console.print()
        for upstream, usage, error in snapshots:
            self.console.print(self.usage_line(upstream, usage, error), soft_wrap=True)
        self.console.print()
        self.console.print()


def fetch_chatgpt_usage(auth_dir: str, auth_file: str) -> tuple[dict[str, Any] | None, str | None]:
    token, account_id, auth_error = load_chatgpt_auth(auth_dir, auth_file)
    if auth_error:
        return None, auth_error

    headers = {
        "Accept": "application/json",
        "Authorization": f"Bearer {token}",
        "User-Agent": "cliproxy-logs",
    }
    if account_id:
        headers["ChatGPT-Account-Id"] = account_id
    try:
        request = urllib.request.Request(CHATGPT_USAGE_URL, headers=headers)
        with urllib.request.urlopen(request, timeout=4) as response:
            data = json.loads(response.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as exc:
        return None, f"HTTP {exc.code}"
    except Exception as exc:
        return None, exc.__class__.__name__
    if isinstance(data, dict):
        return data, None
    return None, "bad-response"


def fetch_anthropic_usage(auth_dir: str, auth_file: str) -> tuple[dict[str, Any] | None, str | None]:
    token, auth_error = load_anthropic_auth(auth_dir, auth_file)
    if auth_error:
        return None, auth_error

    headers = {
        "Accept": "application/json",
        "Authorization": f"Bearer {token}",
        "User-Agent": "cliproxy-logs",
        "anthropic-version": "2023-06-01",
        "anthropic-beta": "oauth-2025-04-20",
    }
    try:
        request = urllib.request.Request(ANTHROPIC_USAGE_URL, headers=headers)
        with urllib.request.urlopen(request, timeout=4) as response:
            data = json.loads(response.read().decode("utf-8", "replace"))
    except urllib.error.HTTPError as exc:
        message = ""
        try:
            body = json.loads(exc.read().decode("utf-8", "replace"))
            raw = body.get("error", {}).get("message") if isinstance(body, dict) else ""
            message = f": {raw.strip()}" if isinstance(raw, str) and raw.strip() else ""
        except Exception:
            pass
        return None, f"HTTP {exc.code}{message}"
    except Exception as exc:
        return None, exc.__class__.__name__
    if isinstance(data, dict):
        return data, None
    return None, "bad-response"


def fetch_usage_snapshots(args: argparse.Namespace) -> list[tuple[str, dict[str, Any] | None, str | None]]:
    return [
        ("chatgpt", *fetch_chatgpt_usage(args.auth_dir, args.codex_auth_file)),
        ("anthropic", *fetch_anthropic_usage(args.auth_dir, args.claude_auth_file)),
    ]


def wait_for_stable_file(path: Path) -> None:
    previous = -1
    for _ in range(8):
        try:
            current = path.stat().st_size
        except FileNotFoundError:
            return
        if current > 0 and current == previous:
            return
        previous = current
        time.sleep(0.3)


class PollingWatcher:
    def __init__(self, log_dir: Path, poll_interval: float) -> None:
        self.log_dir = log_dir
        self.poll_interval = poll_interval
        self.seen = {path.name for path in log_dir.iterdir()}

    def read(self, timeout: float) -> list[Path]:
        time.sleep(max(timeout, self.poll_interval))
        paths: list[Path] = []
        for path in sorted(self.log_dir.iterdir(), key=lambda item: item.stat().st_mtime_ns):
            if path.name in self.seen:
                continue
            self.seen.add(path.name)
            paths.append(path)
        return paths

    def close(self) -> None:
        return None


class InotifyWatcher:
    IN_CREATE = 0x00000100
    IN_MOVED_TO = 0x00000080

    def __init__(self, log_dir: Path) -> None:
        libc_name = ctypes.util.find_library("c")
        if not libc_name:
            raise RuntimeError("libc not found")
        self.libc = ctypes.CDLL(libc_name, use_errno=True)
        flags = getattr(os, "O_NONBLOCK", 0) | getattr(os, "O_CLOEXEC", 0)
        self.fd = self.libc.inotify_init1(flags)
        if self.fd < 0:
            errno = ctypes.get_errno()
            raise OSError(errno, os.strerror(errno))
        watch = self.libc.inotify_add_watch(
            self.fd,
            os.fsencode(log_dir),
            self.IN_CREATE | self.IN_MOVED_TO,
        )
        if watch < 0:
            errno = ctypes.get_errno()
            os.close(self.fd)
            raise OSError(errno, os.strerror(errno))
        self.event_size = struct.calcsize("iIII")
        self.log_dir = log_dir

    @classmethod
    def create(cls, log_dir: Path) -> "InotifyWatcher | None":
        if sys.platform != "linux":
            return None
        try:
            return cls(log_dir)
        except Exception:
            return None

    def read(self, timeout: float) -> list[Path]:
        ready, _, _ = select.select([self.fd], [], [], max(timeout, 0.0))
        if not ready:
            return []
        try:
            data = os.read(self.fd, 65536)
        except BlockingIOError:
            return []

        paths: list[Path] = []
        offset = 0
        while offset + self.event_size <= len(data):
            _wd, _mask, _cookie, name_len = struct.unpack_from("iIII", data, offset)
            offset += self.event_size
            raw_name = data[offset : offset + name_len].split(b"\0", 1)[0]
            offset += name_len
            if raw_name:
                paths.append(self.log_dir / os.fsdecode(raw_name))
        return paths

    def close(self) -> None:
        os.close(self.fd)


def render_file(renderer: GroupedRenderer, path: Path) -> None:
    summary = parse_log(path)
    if summary is not None:
        renderer.render_request(summary)


def live_tail(args: argparse.Namespace, console: Console) -> None:
    log_dir = Path(args.log_dir).expanduser()
    if not log_dir.is_dir():
        console.print(f"[bold red]ERROR:[/] log dir not found: {log_dir}", highlight=False)
        console.print("Make sure CLIProxyAPI is running and request-log: true is set.", style="dim")
        raise SystemExit(1)

    renderer = GroupedRenderer(console)
    watcher = InotifyWatcher.create(log_dir) or PollingWatcher(log_dir, args.poll_interval)
    using_inotify = isinstance(watcher, InotifyWatcher)

    console.print(f"Watching [bold]{log_dir}[/] for new request logs... (Ctrl-C to stop)", highlight=False)
    if not using_inotify:
        console.print("inotify unavailable; polling for new files.", style="dim")
    if args.usage_interval > 0:
        console.print(f"Usage snapshots every {args.usage_interval}s from ChatGPT and Anthropic", style="dim")

    next_usage = time.monotonic() + args.usage_interval if args.usage_interval > 0 else None
    try:
        while True:
            timeout = args.poll_interval
            if next_usage is not None:
                timeout = min(timeout, max(0.0, next_usage - time.monotonic()))
            for path in watcher.read(timeout):
                if not is_request_log(path.name):
                    continue
                wait_for_stable_file(path)
                render_file(renderer, path)

            if next_usage is not None and time.monotonic() >= next_usage:
                renderer.render_usage_block(fetch_usage_snapshots(args))
                next_usage = time.monotonic() + args.usage_interval
    except KeyboardInterrupt:
        console.print()
    finally:
        watcher.close()
        renderer.close_group()


def main() -> int:
    args = parse_args()
    console = Console(highlight=False)
    renderer = GroupedRenderer(console)

    if args.file:
        for raw in args.file:
            render_file(renderer, Path(raw).expanduser())
        renderer.close_group()
        return 0

    live_tail(args, console)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
