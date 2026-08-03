#!/usr/bin/env bash
set -euo pipefail
umask 077

# 定义：
#   将同一份已校验 Markdown 报告先归档到本地，再发送到 Telegram，作为默认
#   的双路投递 provider。
#
# 参数：
#   --report 必填；--local-script 和 --telegram-script 可选，默认使用同目录
#   的 local-file.sh 与 telegram.sh，也可分别由环境变量覆盖。
#
# 输出：
#   保留两个子 adapter 的 stdout 摘要；两路都成功时返回 0，任一路失败时返回
#   非零。不会回显 Telegram 凭据。
#
# 决策：
#   本地投递先建立权威副本；如果文件已存在，local-file.sh 会安全跳过覆盖，
#   随后仍继续尝试 Telegram，从而允许在 Telegram 临时失败后通过下一次运行重试。

usage() {
  cat <<'EOF'
Usage:
  scripts/report-deliveries/local-file-and-telegram.sh --report <path> [options]

Description:
  Save a validated Markdown report locally and send the same report through Telegram.
  Local delivery is authoritative and idempotent; Telegram is retried independently.

Options:
  --report <path>       Required non-empty Markdown report.
  --local-script <path> Local delivery adapter. Default: bundled local-file.sh.
  --telegram-script <path>
                        Telegram delivery adapter. Default: bundled telegram.sh.
  -h, --help            Show this help.

Environment:
  REPORT_LOCAL_DELIVERY_SCRIPT    Optional local adapter override.
  REPORT_TELEGRAM_DELIVERY_SCRIPT Optional Telegram adapter override.
  Other REPORT_* and integration variables are forwarded to both adapters.

Outputs:
  stdout                Summaries from local and Telegram delivery.
  stderr                Delivery errors without credentials.
  exit 0                Both delivery adapters succeeded.
  exit non-zero         Invalid input or either delivery adapter failed.

Examples:
  scripts/report-deliveries/local-file-and-telegram.sh --report report.md
  scripts/report-deliveries/local-file-and-telegram.sh --report report.md \
    --local-script ./local-file.sh --telegram-script ./telegram.sh
EOF
}

log_error() { printf 'ERROR: %s\n' "$*" >&2; }

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
report_path=${REPORT_PATH:-}
local_script=${REPORT_LOCAL_DELIVERY_SCRIPT:-"${script_dir}/local-file.sh"}
telegram_script=${REPORT_TELEGRAM_DELIVERY_SCRIPT:-"${script_dir}/telegram.sh"}

while (($#)); do
  case "$1" in
    --report)
      [[ $# -ge 2 ]] || { log_error "--report requires a value"; exit 1; }
      report_path=$2
      shift 2
      ;;
    --local-script)
      [[ $# -ge 2 ]] || { log_error "--local-script requires a value"; exit 1; }
      local_script=$2
      shift 2
      ;;
    --telegram-script)
      [[ $# -ge 2 ]] || { log_error "--telegram-script requires a value"; exit 1; }
      telegram_script=$2
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
for adapter in "$local_script" "$telegram_script"; do
  if [[ ! -x "$adapter" ]]; then
    log_error "Delivery adapter is not executable: $adapter"
    exit 1
  fi
done

if ! "$local_script" --report "$report_path"; then
  log_error "Local file delivery failed; Telegram delivery was skipped."
  exit 1
fi

if ! "$telegram_script" --report "$report_path"; then
  log_error "Telegram delivery failed; the local report remains authoritative."
  exit 1
fi

printf 'Combined local-file and Telegram delivery succeeded.\n'
