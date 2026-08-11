#!/usr/bin/env bash
set -euo pipefail

# Definition:
#   Deliver a generated Markdown work report through a Telegram bot.
#
# Parameters:
#   --report is required. --config defaults to REPORT_DELIVERY_CONFIG or the
#   agent-notify user config. Environment tokens take precedence over config.
#
# Outputs:
#   Sends one or more Telegram messages and prints a success summary to stdout.
#   Exits non-zero without sending when configuration or input is invalid.

usage() {
  cat <<'EOF'
Usage:
  scripts/report-deliveries/telegram.sh --report <path> [options]

Description:
  Convert a Markdown work report to Telegram-safe HTML and deliver it through
  TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID or agent-notify JSON configuration.

Options:
  --report <path>  Required Markdown report.
  --config <path>  Optional JSON config. Default: REPORT_DELIVERY_CONFIG or
                   ~/.config/agent-notify/config.json.
  --title <value>  Message title. Default: REPORT_TITLE or Agent 工作报告.
  -h, --help        Show this help.

Outputs:
  stdout            Delivery summary without credentials.
  stderr            Validation or Telegram API errors.
  exit 0            Every report chunk was delivered.
  exit non-zero     Invalid input/configuration or delivery failure.

Examples:
  scripts/report-deliveries/telegram.sh --report report.md
  TELEGRAM_BOT_TOKEN=... TELEGRAM_CHAT_ID=... \
    scripts/report-deliveries/telegram.sh --report report.md --title "昨日工作报告"
EOF
}

log_error() { printf 'ERROR: %s\n' "$*" >&2; }

report_path=${REPORT_PATH:-}
config_path=${REPORT_DELIVERY_CONFIG:-"${HOME}/.config/agent-notify/config.json"}
report_title=${REPORT_TITLE:-Agent 工作报告}

while (($#)); do
  case "$1" in
    --report)
      [[ $# -ge 2 ]] || { log_error "--report requires a value"; exit 1; }
      report_path=$2
      shift 2
      ;;
    --config)
      [[ $# -ge 2 ]] || { log_error "--config requires a value"; exit 1; }
      config_path=$2
      shift 2
      ;;
    --title)
      [[ $# -ge 2 ]] || { log_error "--title requires a value"; exit 1; }
      report_title=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      log_error "Unknown option: $1"
      log_error "Run with --help for usage."
      exit 1
      ;;
  esac
done

if [[ -z "$report_path" ]]; then
  log_error "--report is required"
  exit 1
fi
if [[ ! -s "$report_path" ]]; then
  log_error "Report file is missing or empty: $report_path"
  exit 1
fi
for dependency in jq curl python3; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    log_error "Required command not found: $dependency"
    exit 1
  fi
done

bot_token=${TELEGRAM_BOT_TOKEN:-}
chat_id=${TELEGRAM_CHAT_ID:-}
if [[ (-z "$bot_token" || -z "$chat_id") && -f "$config_path" ]]; then
  [[ -n "$bot_token" ]] || bot_token=$(jq -r '.telegram.bot_token // ""' "$config_path")
  [[ -n "$chat_id" ]] || chat_id=$(jq -r '.telegram.chat_id // ""' "$config_path")
fi
if [[ -z "$bot_token" || -z "$chat_id" ]]; then
  log_error "Telegram bot token or chat id is missing in env/config"
  exit 1
fi

# Telegram rich text uses HTML parse mode. Convert and chunk locally so report
# adapters never expose credentials or raw Markdown formatting to another tool.
python3 - "$report_path" "$report_title" <<'PY' | while IFS= read -r chunk_json; do
import html
import json
import pathlib
import re
import sys


def inline_markdown(text: str) -> str:
    escaped = html.escape(text, quote=False)
    escaped = re.sub(r"`([^`]+)`", lambda m: f"<code>{m.group(1)}</code>", escaped)
    escaped = re.sub(r"\*\*([^*]+)\*\*", lambda m: f"<b>{m.group(1)}</b>", escaped)
    return escaped


def markdown_to_telegram_html(markdown: str, title: str) -> str:
    lines = [f"<b>{html.escape(title, quote=False)}</b>", ""]
    for raw_line in markdown.strip().splitlines():
        line = raw_line.strip()
        if line == "---":
            lines.append("")
            continue
        if line.startswith("## "):
            lines.append(f"<b>{inline_markdown(line[3:].strip())}</b>")
            continue
        if raw_line.startswith("    - "):
            lines.append(f"↳ {inline_markdown(line[2:].strip())}")
            continue
        if line.startswith("- "):
            lines.append(f"• {inline_markdown(line[2:].strip())}")
            continue
        if line:
            lines.append(inline_markdown(line))
        else:
            lines.append("")
    return "\n".join(lines).strip()


def emit_chunks(payload: str) -> None:
    limit = 3600
    current = ""
    for line in payload.splitlines():
        candidate = line if not current else current + "\n" + line
        if len(candidate) > limit and current:
            print(json.dumps(current, ensure_ascii=False))
            current = line
        else:
            current = candidate
    if current:
        print(json.dumps(current, ensure_ascii=False))


path = pathlib.Path(sys.argv[1])
title = sys.argv[2]
text = path.read_text(encoding="utf-8")
emit_chunks(markdown_to_telegram_html(text, title))
PY
  message=$(jq -r . <<<"$chunk_json")
  curl --fail-with-body --silent --show-error \
    --data-urlencode "chat_id=${chat_id}" \
    --data-urlencode "text=${message}" \
    --data-urlencode "parse_mode=HTML" \
    --data-urlencode "disable_web_page_preview=true" \
    "https://api.telegram.org/bot${bot_token}/sendMessage" >/dev/null
done

printf 'Telegram report sent.\n'
