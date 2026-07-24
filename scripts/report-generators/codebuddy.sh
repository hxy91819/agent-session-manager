#!/usr/bin/env bash
set -euo pipefail

# Definition:
#   Generate one Markdown work report with CodeBuddy from a prepared prompt.
#
# Parameters:
#   --prompt is required. --model is optional. --max-turns defaults to 50.
#   REPORT_MODEL, REPORT_MAX_TURNS, and REPORT_CODEBUDDY_BIN provide adapter
#   defaults when the script is called by daily-agent-report.sh.
#
# Outputs:
#   Writes the generated Markdown to stdout and diagnostics to stderr.
#   Exits non-zero when input validation or CodeBuddy generation fails.

usage() {
  cat <<'EOF'
Usage:
  scripts/report-generators/codebuddy.sh --prompt <path> [options]

Description:
  Use CodeBuddy to generate a Markdown work report from a self-contained prompt.
  The adapter disables tools because report evidence may contain untrusted text.

Options:
  --prompt <path>         Required prompt file.
  --model <value>         Optional model. Default: REPORT_MODEL or CodeBuddy config.
  --max-turns <n>         Maximum turns. Default: REPORT_MAX_TURNS or 50.
  --codebuddy-bin <path>  CodeBuddy executable. Default: REPORT_CODEBUDDY_BIN or codebuddy.
  -h, --help              Show this help.

Outputs:
  stdout                  Generated Markdown report.
  stderr                  CodeBuddy diagnostics.
  exit 0                  Generation succeeded.
  exit non-zero           Invalid input, missing dependency, or generation failure.

Examples:
  scripts/report-generators/codebuddy.sh --prompt prompt.txt
  scripts/report-generators/codebuddy.sh --prompt prompt.txt --model my-model --max-turns 50
EOF
}

log_error() { printf 'ERROR: %s\n' "$*" >&2; }

prompt_path=${REPORT_PROMPT_PATH:-}
model=${REPORT_MODEL:-}
max_turns=${REPORT_MAX_TURNS:-50}
codebuddy_bin=${REPORT_CODEBUDDY_BIN:-codebuddy}

while (($#)); do
  case "$1" in
    --prompt)
      [[ $# -ge 2 ]] || { log_error "--prompt requires a value"; exit 1; }
      prompt_path=$2
      shift 2
      ;;
    --model)
      [[ $# -ge 2 ]] || { log_error "--model requires a value"; exit 1; }
      model=$2
      shift 2
      ;;
    --max-turns)
      [[ $# -ge 2 ]] || { log_error "--max-turns requires a value"; exit 1; }
      max_turns=$2
      shift 2
      ;;
    --codebuddy-bin)
      [[ $# -ge 2 ]] || { log_error "--codebuddy-bin requires a value"; exit 1; }
      codebuddy_bin=$2
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

if [[ -z "$prompt_path" ]]; then
  log_error "--prompt is required"
  exit 1
fi
if [[ ! -f "$prompt_path" ]]; then
  log_error "Prompt file not found: $prompt_path"
  exit 1
fi
if ! [[ "$max_turns" =~ ^[1-9][0-9]*$ ]]; then
  log_error "--max-turns must be a positive integer"
  exit 1
fi
if ! command -v "$codebuddy_bin" >/dev/null 2>&1; then
  log_error "CodeBuddy executable not found: $codebuddy_bin"
  exit 1
fi

# Report evidence is untrusted input. Supplying all context through stdin and
# disabling tools prevents embedded instructions from reaching host resources.
codebuddy_args=(--print --permission-mode dontAsk --tools "" --strict-mcp-config --max-turns "$max_turns")
if [[ -n "$model" ]]; then
  codebuddy_args+=(--model "$model")
fi

"$codebuddy_bin" "${codebuddy_args[@]}" < "$prompt_path"
