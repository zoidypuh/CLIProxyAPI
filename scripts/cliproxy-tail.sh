#!/usr/bin/env bash
# Live-tail CLIProxyAPI request logs in LM-Studio style.
# Each upstream call lands as its own file under ~/.cli-proxy-api/logs/.
# This script watches for new files and prints a compact, human summary
# (timestamp, model, message count, input/cached/output/total tokens, finish).
#
# Usage:  ~/cliproxy-tail.sh
#         CLIPROXY_LOG_DIR=/some/other/path ~/cliproxy-tail.sh

set -euo pipefail

LOG_DIR="${CLIPROXY_LOG_DIR:-$HOME/.cli-proxy-api/logs}"

if [[ ! -d "$LOG_DIR" ]]; then
  echo "ERROR: log dir not found: $LOG_DIR" >&2
  echo "Make sure CLIProxyAPI is running and request-log: true is set." >&2
  exit 1
fi

is_request_log() {
  case "$1" in
    v1-*|claude-*|gemini-*|codex-*|openai-*|anthropic-*) return 0 ;;
    *) return 1 ;;
  esac
}

PRETTY=$(cat <<'PYEOF'
import colorsys, hashlib, json, os, re, sys
from datetime import datetime
from urllib.parse import urlparse

try:
    from rich.console import Console
    from rich.text import Text
    RICH = True
except Exception:
    Console = None
    Text = None
    RICH = False

path = sys.argv[1]
try:
    with open(path, 'r', errors='replace') as f:
        text = f.read()
except FileNotFoundError:
    sys.exit(0)

_SECTION_RE = re.compile(r'^=== ([A-Z0-9 ]+) ===\s*$', re.M)
def section(name, last=False):
    # Find real top-level section markers (line-anchored), pick first or last.
    matches = [m for m in _SECTION_RE.finditer(text) if m.group(1).strip() == name]
    if not matches:
        return ''
    m = matches[-1] if last else matches[0]
    start = m.end()
    # End at next real section marker or EOF
    nxt = _SECTION_RE.search(text, start)
    end = nxt.start() if nxt else len(text)
    return text[start:end].strip()

def first_json(blob):
    blob = blob.strip()
    if not blob:
        return None
    # Strip "Status: 200" header lines etc., but preserve SSE payloads.
    lines = blob.splitlines()
    while lines and not lines[0].lstrip().startswith(('{', '[', 'data:', 'event:')):
        lines.pop(0)
    blob = '\n'.join(lines).strip()
    # SSE? aggregate usage/finish across all chunks; pick last chunk with usage.
    if blob.startswith('data:') or '\ndata:' in blob or blob.startswith('event:'):
        merged = {'usage': {}, 'choices': [{}], 'model': None, 'stop_reason': None}
        last_with_usage = None
        for line in blob.splitlines():
            line = line.strip()
            if not line.startswith('data:'):
                continue
            payload = line[5:].strip()
            if not payload or payload == '[DONE]':
                continue
            try:
                obj = json.loads(payload)
            except Exception:
                continue
            if not isinstance(obj, dict):
                continue
            if obj.get('model'):
                merged['model'] = obj['model']
            response_obj = obj.get('response')
            if isinstance(response_obj, dict):
                if response_obj.get('model'):
                    merged['model'] = response_obj['model']
            ch = obj.get('choices') or []
            if ch and isinstance(ch, list):
                fr = ch[0].get('finish_reason')
                if fr:
                    merged['choices'][0]['finish_reason'] = fr
            if obj.get('stop_reason'):
                merged['stop_reason'] = obj['stop_reason']
            # Anthropic message_delta nests usage under delta sometimes
            usage = obj.get('usage')
            if not usage and isinstance(obj.get('delta'), dict):
                usage = obj['delta'].get('usage')
            # Anthropic: top-level message in message_start has usage
            if not usage and isinstance(obj.get('message'), dict):
                usage = obj['message'].get('usage')
            # OpenAI Responses streams usage on the final response.completed event.
            if not usage and isinstance(response_obj, dict):
                usage = response_obj.get('usage')
            if usage:
                merged['usage'].update(usage)
                last_with_usage = obj
            # Anthropic message_delta: stop_reason inside delta
            d = obj.get('delta')
            if isinstance(d, dict) and d.get('stop_reason'):
                merged['stop_reason'] = d['stop_reason']
        if merged['usage'] or merged['model'] or merged['choices'][0].get('finish_reason'):
            return merged
        return None
    try:
        return json.loads(blob)
    except Exception:
        # Try to truncate to last balanced brace
        depth = 0; end = -1
        for i, c in enumerate(blob):
            if c == '{': depth += 1
            elif c == '}':
                depth -= 1
                if depth == 0:
                    end = i + 1
        if end > 0:
            try: return json.loads(blob[:end])
            except Exception: return None
    return None

def parse_header_block(blob):
    headers = {}
    for raw in blob.splitlines():
        if ':' not in raw:
            continue
        key, value = raw.split(':', 1)
        key = key.strip().lower()
        value = value.strip()
        if key:
            headers[key] = value
    return headers

def compact_user_agent(value):
    value = re.sub(r'\s+', ' ', (value or '').strip())
    if not value:
        return ''
    if len(value) <= 48:
        return value
    return value[:45] + '...'

def client_key(headers):
    raw = headers.get('authorization') or headers.get('x-api-key') or headers.get('api-key') or ''
    raw = raw.strip()
    if not raw:
        return ''
    if raw.lower().startswith('bearer '):
        raw = raw[7:].strip()
    return raw.strip()

def client_label(headers):
    key = client_key(headers)
    key_lower = key.lower()
    if key_lower in {'codex', 'co...ex'}:
        return 'Codex CLI'
    if key_lower in {'hermes', 'he...es', 'cliproxy', 'cl...xy'}:
        return 'Hermes Agent'

    ua = compact_user_agent(headers.get('user-agent', ''))
    return ua

info = section('REQUEST INFO')
ts_match = re.search(r'Timestamp:\s*([0-9T:\-\.]+)', info)
ts = ts_match.group(1) if ts_match else ''
try:
    start_dt = datetime.fromisoformat(ts.replace('Z', '+00:00'))
except Exception:
    start_dt = datetime.now()
stamp = start_dt.strftime('%H:%M:%S')

method_match = re.search(r'Method:\s*(\S+)', info)
method = method_match.group(1) if method_match else ''

url_match = re.search(r'URL:\s*(\S+)', info)
endpoint = url_match.group(1) if url_match else os.path.basename(path)

req = first_json(section('REQUEST BODY'))
downstream_headers = parse_header_block(section('HEADERS'))
response_text = section('RESPONSE', last=True)
api_response_text = section('API RESPONSE 1', last=True)
resp = first_json(response_text) or first_json(api_response_text)

status_match = re.search(r'\bStatus:\s*(\d+)', response_text or api_response_text)
status = status_match.group(1) if status_match else ''

api_req = section('API REQUEST 1')
upstream_match = re.search(r'Upstream URL:\s*(\S+)', api_req)
upstream = upstream_match.group(1) if upstream_match else ''
auth_match = re.search(r'Auth:\s*([^\n]+)', api_req)
auth = auth_match.group(1).strip() if auth_match else ''
provider_match = re.search(r'provider=([^,\s]+)', auth)
provider = provider_match.group(1) if provider_match else ''

resp_ts_match = re.search(r'Timestamp:\s*([0-9T:\-\.]+)', api_response_text or response_text)
duration = None
if resp_ts_match:
    try:
        end_dt = datetime.fromisoformat(resp_ts_match.group(1).replace('Z', '+00:00'))
        duration = max(0.0, (end_dt - start_dt).total_seconds())
    except Exception:
        duration = None

model = (req or {}).get('model') or (resp or {}).get('model') or 'unknown'
messages = (req or {}).get('messages') or []
msg_count = len(messages) if isinstance(messages, list) else 0

# Token usage — handle OpenAI chat, OpenAI responses, and Anthropic-style usage.
prompt_tok = compl_tok = cached_tok = total_tok = reasoning_tok = None
finish = None
if isinstance(resp, dict):
    usage = resp.get('usage') or {}
    if 'prompt_tokens' in usage:
        prompt_tok = usage.get('prompt_tokens')
        compl_tok = usage.get('completion_tokens')
        total_tok = usage.get('total_tokens')
        details = usage.get('prompt_tokens_details') or {}
        cached_tok = details.get('cached_tokens')
        completion_details = usage.get('completion_tokens_details') or {}
        reasoning_tok = completion_details.get('reasoning_tokens')
    elif 'input_tokens' in usage:
        prompt_tok = usage.get('input_tokens')
        input_details = usage.get('input_tokens_details') or {}
        cached_tok = input_details.get('cached_tokens')

        # Anthropic splits cache tokens out from input_tokens; Responses includes
        # cached tokens inside input_tokens and reports the cached subset in details.
        if 'cache_read_input_tokens' in usage or 'cache_creation_input_tokens' in usage:
            prompt_tok = (usage.get('input_tokens') or 0) \
                       + (usage.get('cache_read_input_tokens') or 0) \
                       + (usage.get('cache_creation_input_tokens') or 0)
            cached_tok = usage.get('cache_read_input_tokens') or cached_tok

        compl_tok = usage.get('output_tokens')
        output_details = usage.get('output_tokens_details') or {}
        reasoning_tok = output_details.get('reasoning_tokens')
        total_tok = usage.get('total_tokens')
        if prompt_tok is not None and compl_tok is not None:
            total_tok = total_tok if total_tok is not None else prompt_tok + compl_tok
    choices = resp.get('choices') or []
    if choices and isinstance(choices, list):
        finish = choices[0].get('finish_reason')
    if not finish:
        finish = resp.get('stop_reason')

def fmt_num(value):
    if value is None:
        return '-'
    try:
        return f"{int(value):,}"
    except Exception:
        return str(value)

def fmt_duration(value):
    if value is None:
        return ''
    if value < 10:
        return f"{value:.1f}s"
    return f"{value:.0f}s"

def stable_style(name):
    digest = hashlib.sha1(name.encode("utf-8", "replace")).digest()
    hue = int.from_bytes(digest[:2], "big") / 65535
    saturation = 0.68 + (digest[2] / 255) * 0.18
    lightness = 0.62 + (digest[3] / 255) * 0.14
    red, green, blue = colorsys.hls_to_rgb(hue, lightness, saturation)
    return f"#{int(red * 255):02x}{int(green * 255):02x}{int(blue * 255):02x}"

def request_color_key(model_name, client_name):
    return f"{model_name or 'unknown'}\0{client_name or 'unknown'}"

def request_color_style(model_name, client_name):
    return stable_style(request_color_key(model_name, client_name))

def plain_style_enabled():
    return sys.stdout.isatty() and os.environ.get("NO_COLOR") is None

def ansi(style):
    if not plain_style_enabled():
        return ""
    if style.startswith("#") and len(style) == 7:
        try:
            red = int(style[1:3], 16)
            green = int(style[3:5], 16)
            blue = int(style[5:7], 16)
            return f"\033[38;2;{red};{green};{blue}m"
        except ValueError:
            return ""
    codes = {
        "bright_cyan": "96", "bright_magenta": "95", "bright_green": "92",
        "bright_blue": "94", "bright_yellow": "93", "bright_red": "91",
        "bold": "1", "dim": "2",
    }
    return f"\033[{codes.get(style, '0')}m"

def reset():
    return "\033[0m" if plain_style_enabled() else ""

def color(text, style):
    code = ansi(style)
    return f"{code}{text}{reset()}" if code else text

headline_parts = []
if status:
    headline_parts.append(f"status={status}")
if duration is not None:
    headline_parts.append(f"duration={fmt_duration(duration)}")
if finish:
    headline_parts.append(f"finish={finish}")

client = client_label(downstream_headers)

token_parts = []
if prompt_tok is not None:
    fresh_tok = prompt_tok
    if cached_tok:
        try:
            fresh_tok = max(0, int(prompt_tok) - int(cached_tok))
        except Exception:
            fresh_tok = prompt_tok
    token_parts.append(("fresh", fmt_num(fresh_tok)))
if cached_tok:
    token_parts.append(("cached", fmt_num(cached_tok)))
if compl_tok is not None:
    token_parts.append(("output", fmt_num(compl_tok)))
if reasoning_tok:
    token_parts.append(("reasoning", fmt_num(reasoning_tok)))
if total_tok is not None:
    token_parts.append(("total", fmt_num(total_tok)))

request_style = request_color_style(model, client)

if RICH:
    console = Console(highlight=False)
    first = Text()
    first.append(stamp, style=request_style)
    first.append("  ")
    first.append("INFO", style=f"bold {request_style}")
    first.append("  ")
    first.append(model, style=f"bold {request_style}")
    if headline_parts:
        first.append("  ")
        first.append("  ".join(headline_parts), style=request_style)
    console.print(first, soft_wrap=True)
    second = Text()
    second.append(" " * 8)
    second.append("route", style=request_style)
    second.append("   ")
    if method:
        second.append(method, style=request_style)
        second.append("  ")
    second.append(endpoint, style=request_style)
    if msg_count:
        second.append("  ")
        second.append(f"messages={msg_count}", style=request_style)
    if client:
        second.append("  ")
        second.append(f"client={client}", style=request_style)
    if upstream:
        parsed = urlparse(upstream)
        second.append("  ")
        second.append(f"upstream={parsed.netloc or upstream}", style=request_style)
    console.print(second, soft_wrap=True)
    if token_parts:
        third = Text()
        third.append(" " * 8)
        third.append("tokens", style=request_style)
        third.append("  ")
        for idx, (name, value) in enumerate(token_parts):
            if idx:
                third.append("  ")
            third.append(f"{name}=", style=request_style)
            third.append(value, style=request_style)
        console.print(third, soft_wrap=True)
    console.print()
else:
    request_color = ansi(request_style)
    headline = "  ".join(headline_parts)
    print(f"{request_color}{stamp}  INFO  {model}  {headline}{reset()}")
    rendered_route = []
    if method:
        rendered_route.append(color(method, request_style))
    rendered_route.append(color(endpoint, request_style))
    if msg_count:
        rendered_route.append(color(f"messages={msg_count}", request_style))
    if client:
        rendered_route.append(color(f"client={client}", request_style))
    if upstream:
        parsed = urlparse(upstream)
        rendered_route.append(color(f"upstream={parsed.netloc or upstream}", request_style))
    print(f"{request_color}        route   {'  '.join(rendered_route)}{reset()}")
    if token_parts:
        rendered_tokens = [
            f"{color(name + '=', request_style)}{color(value, request_style)}"
            for name, value in token_parts
        ]
        print(f"{request_color}        tokens  {'  '.join(rendered_tokens)}{reset()}")
    print()
PYEOF
)

print_file() {
  local path="$1"
  # Wait briefly until the proxy finishes writing the file (size stable for 1 cycle).
  local prev=-1 cur
  for _ in 1 2 3 4 5 6 7 8; do
    cur=$(stat -c%s -- "$path" 2>/dev/null || echo 0)
    if [[ "$cur" -gt 0 && "$cur" == "$prev" ]]; then break; fi
    prev="$cur"
    sleep 0.3
  done
  python3 -c "$PRETTY" "$path" 2>/dev/null || true
}

echo "Watching $LOG_DIR for new request logs... (Ctrl-C to stop)"

if command -v inotifywait >/dev/null 2>&1; then
  inotifywait -m -q -e create -e moved_to --format '%f' "$LOG_DIR" \
  | while IFS= read -r fname; do
      if is_request_log "$fname"; then
        print_file "$LOG_DIR/$fname"
      fi
    done
else
  echo "(inotifywait not found — polling at 0.5s. apt install inotify-tools for instant updates.)" >&2
  declare -A seen=()
  for f in "$LOG_DIR"/*; do
    [[ -e "$f" ]] && seen["$(basename "$f")"]=1
  done
  while true; do
    for f in "$LOG_DIR"/*; do
      [[ -e "$f" ]] || continue
      bn="$(basename "$f")"
      if [[ -z "${seen[$bn]:-}" ]]; then
        seen["$bn"]=1
        if is_request_log "$bn"; then
          print_file "$f"
        fi
      fi
    done
    sleep 0.5
  done
fi
