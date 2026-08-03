#!/usr/bin/env bash
set -euo pipefail
umask 077

# 定义：
#   通过 Ollama Cloud 的 OpenAI 兼容接口，根据预先生成的 prompt 输出一份
#   Markdown 工作报告。它与 CodeBuddy provider 共享同一个 stdin/stdout 契约。
#
# 参数：
#   --prompt 必填；--model、--base-url、--timeout-seconds 可选，分别默认取
#   REPORT_MODEL/OLLAMA_MODEL、REPORT_OLLAMA_BASE_URL/OLLAMA_BASE_URL、
#   REPORT_OLLAMA_REASONING_EFFORT/OLLAMA_REASONING_EFFORT 和 300 秒。
#
# 输出：
#   stdout 只输出模型返回的 Markdown；stderr 输出诊断；非 2xx 响应或无有效
#   assistant 内容时返回非零。API key 只从环境变量读取，不接受命令行参数。
#
# 决策：
#   使用 /v1/chat/completions 而不是本地 Ollama SDK，避免定时任务依赖 Python
#   第三方包，同时保持与用户提供的 Ollama Cloud API 地址一致。OpenAI 兼容
#   接口用 reasoning_effort 表达思考深度，默认设为 max。

usage() {
  cat <<'EOF'
Usage:
  scripts/report-generators/ollama.sh --prompt <path> [options]

Description:
  Generate a Markdown work report with the Ollama Cloud OpenAI-compatible API.
  The prompt is sent as one user message and report Markdown is written to stdout.

Options:
  --prompt <path>          Required UTF-8 prompt file.
  --model <value>          Model. Default: REPORT_MODEL, OLLAMA_MODEL, or
                           deepseek-v4-flash:0731-cloud.
  --base-url <value>       API base URL. Default: REPORT_OLLAMA_BASE_URL,
                           OLLAMA_BASE_URL, or https://ollama.com/v1.
  --reasoning-effort <value>
                           Thinking depth. Default: REPORT_OLLAMA_REASONING_EFFORT,
                           OLLAMA_REASONING_EFFORT, or max.
  --timeout-seconds <n>    Request timeout. Default: REPORT_OLLAMA_TIMEOUT_SECONDS
                           or 300.
  -h, --help               Show this help.

Environment:
  OLLAMA_API_KEY           Required API key. It is never accepted as a CLI flag.
  OLLAMA_MODEL             Optional model override.
  OLLAMA_BASE_URL          Optional API base URL ending in /v1.
  OLLAMA_REASONING_EFFORT  Optional thinking depth: none, low, medium, high, or max.
  OLLAMA_CURL_BIN          Optional curl executable override for tests or wrappers.

Outputs:
  stdout                   Generated Markdown report only.
  stderr                   Non-secret request diagnostics.
  exit 0                   A non-empty assistant message was returned.
  exit non-zero            Invalid input, missing dependency, network, HTTP, or
                           response-format failure.

Examples:
  scripts/report-generators/ollama.sh --prompt report-prompt.txt
  scripts/report-generators/ollama.sh --prompt report-prompt.txt --model my-model
  scripts/report-generators/ollama.sh --prompt report-prompt.txt --reasoning-effort max
EOF
}

log_error() { printf 'ERROR: %s\n' "$*" >&2; }

prompt_path=${REPORT_PROMPT_PATH:-}
model=${REPORT_MODEL:-${OLLAMA_MODEL:-deepseek-v4-flash:0731-cloud}}
base_url=${REPORT_OLLAMA_BASE_URL:-${OLLAMA_BASE_URL:-https://ollama.com/v1}}
api_key=${REPORT_OLLAMA_API_KEY:-${OLLAMA_API_KEY:-}}
reasoning_effort=${REPORT_OLLAMA_REASONING_EFFORT:-${OLLAMA_REASONING_EFFORT:-max}}
timeout_seconds=${REPORT_OLLAMA_TIMEOUT_SECONDS:-300}
curl_bin=${OLLAMA_CURL_BIN:-curl}

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
    --base-url)
      [[ $# -ge 2 ]] || { log_error "--base-url requires a value"; exit 1; }
      base_url=$2
      shift 2
      ;;
    --reasoning-effort)
      [[ $# -ge 2 ]] || { log_error "--reasoning-effort requires a value"; exit 1; }
      reasoning_effort=$2
      shift 2
      ;;
    --timeout-seconds)
      [[ $# -ge 2 ]] || { log_error "--timeout-seconds requires a value"; exit 1; }
      timeout_seconds=$2
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
if [[ -z "$model" ]]; then
  log_error "Ollama model is empty; set --model or OLLAMA_MODEL"
  exit 1
fi
if [[ -z "$api_key" ]]; then
  log_error "OLLAMA_API_KEY is required"
  exit 1
fi
if [[ ! "$base_url" =~ ^https?://[^[:space:]]+$ ]]; then
  log_error "Ollama base URL must start with http:// or https://"
  exit 1
fi
if [[ ! "$reasoning_effort" =~ ^(none|low|medium|high|max)$ ]]; then
  log_error "Ollama reasoning effort must be one of: none, low, medium, high, max"
  exit 1
fi
if ! [[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]]; then
  log_error "--timeout-seconds must be a positive integer"
  exit 1
fi
if [[ "$curl_bin" == */* ]]; then
  if [[ ! -x "$curl_bin" ]]; then
    log_error "curl executable not found or not executable: $curl_bin"
    exit 1
  fi
elif ! command -v "$curl_bin" >/dev/null 2>&1; then
  log_error "curl executable not found: $curl_bin"
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  log_error "Required command not found: jq"
  exit 1
fi

request_path=$(mktemp)
response_path=$(mktemp)
curl_config_path=$(mktemp)
cleanup() {
  rm -f -- "$request_path" "$response_path" "$curl_config_path"
}
trap cleanup EXIT

if ! jq -n \
  --arg model "$model" \
  --arg reasoning_effort "$reasoning_effort" \
  --rawfile prompt "$prompt_path" \
  '{model: $model, messages: [{role: "user", content: $prompt}], reasoning_effort: $reasoning_effort}' \
  > "$request_path"; then
  log_error "Unable to build Ollama request payload"
  exit 1
fi

# 将认证头放进临时 curl 配置，避免密钥出现在进程参数和诊断日志中。
{
  printf 'header = "Authorization: Bearer %s"\n' "$api_key"
  printf 'header = "Content-Type: application/json"\n'
} > "$curl_config_path"

endpoint="${base_url%/}/chat/completions"
http_status=
if ! http_status=$(
  "$curl_bin" \
    --config "$curl_config_path" \
    --silent \
    --show-error \
    --location \
    --connect-timeout 15 \
    --max-time "$timeout_seconds" \
    --output "$response_path" \
    --write-out '%{http_code}' \
    --data-binary "@$request_path" \
    "$endpoint"
); then
  log_error "Ollama API request failed"
  exit 1
fi

if ! [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
  error_message=$(jq -r '.error.message // .message // empty' "$response_path" 2>/dev/null || true)
  if [[ -n "$error_message" ]]; then
    log_error "Ollama API returned HTTP ${http_status}: ${error_message}"
  else
    log_error "Ollama API returned HTTP ${http_status}"
  fi
  exit 1
fi

if ! jq -er '.choices[0].message.content | select(type == "string" and length > 0)' "$response_path"; then
  log_error "Ollama API response did not contain assistant Markdown"
  exit 1
fi
