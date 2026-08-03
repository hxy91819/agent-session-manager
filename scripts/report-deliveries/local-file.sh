#!/usr/bin/env bash
set -euo pipefail
umask 077

# 定义：
#   将经过校验的 Markdown 工作报告按统计窗口归档到本地目录，作为默认投递
#   provider，方便后续直接查看和检索。
#
# 参数：
#   --report 必填；--output-dir、--key、--kind、--window-start 可选，默认分别
#   取 REPORT_LOCAL_REPORT_DIR、REPORT_DELIVERY_KEY、REPORT_REPORT_KIND 和
#   REPORT_WINDOW_START。
#
# 输出：
#   在目标目录生成一个可追踪的 .md 文件；已有同名常规文件时保持原文件并返回 0。
#   stdout 输出写入/跳过摘要，stderr 输出错误，失败时返回非零。
#
# 决策：
#   文件名由报告类型和统计窗口开始日期决定，而不是生成时间决定；使用原子硬
#   链接落盘，避免并发运行产生重复文件，并把已存在的本地常规文件视为权威版本。
#   目标符号链接直接拒绝，避免跟随外部路径后静默丢弃报告。

usage() {
  cat <<'EOF'
Usage:
  scripts/report-deliveries/local-file.sh --report <path> [options]

Description:
  Save a validated Markdown work report to a local directory. Delivery is
  idempotent per report kind and reporting-window start date.

Options:
  --report <path>          Required non-empty Markdown report.
  --output-dir <path>     Destination directory. Default: REPORT_LOCAL_REPORT_DIR
                           or .local/agent-work-reports.
  --kind <daily|weekly>   Report kind. Default: REPORT_REPORT_KIND or daily.
  --window-start <time>   Window start used for the canonical filename. Default:
                           REPORT_WINDOW_START.
  --key <value>           Explicit safe filename stem, without .md.
  -h, --help              Show this help.

Outputs:
  <output-dir>/<key>.md   Canonical local Markdown report.
  stdout                  Write or already-recorded summary.
  exit 0                  Report written or existing local report retained.
  exit non-zero            Invalid input, filesystem, or atomic-write failure.

Examples:
  scripts/report-deliveries/local-file.sh --report report.md
  scripts/report-deliveries/local-file.sh --report report.md \
    --output-dir .local/agent-work-reports --key daily-2026-07-31
EOF
}

log_error() { printf 'ERROR: %s\n' "$*" >&2; }

report_path=${REPORT_PATH:-}
output_dir=${REPORT_LOCAL_REPORT_DIR:-.local/agent-work-reports}
report_kind=${REPORT_REPORT_KIND:-daily}
window_start=${REPORT_WINDOW_START:-}
delivery_key=${REPORT_DELIVERY_KEY:-}

while (($#)); do
  case "$1" in
    --report)
      [[ $# -ge 2 ]] || { log_error "--report requires a value"; exit 1; }
      report_path=$2
      shift 2
      ;;
    --output-dir)
      [[ $# -ge 2 ]] || { log_error "--output-dir requires a value"; exit 1; }
      output_dir=$2
      shift 2
      ;;
    --kind)
      [[ $# -ge 2 ]] || { log_error "--kind requires a value"; exit 1; }
      report_kind=$2
      shift 2
      ;;
    --window-start)
      [[ $# -ge 2 ]] || { log_error "--window-start requires a value"; exit 1; }
      window_start=$2
      shift 2
      ;;
    --key)
      [[ $# -ge 2 ]] || { log_error "--key requires a value"; exit 1; }
      delivery_key=$2
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
if [[ "$report_kind" != "daily" && "$report_kind" != "weekly" ]]; then
  log_error "Report kind must be daily or weekly: $report_kind"
  exit 1
fi

if [[ -z "$delivery_key" ]]; then
  if [[ -n "$window_start" ]]; then
    window_date=${window_start%%T*}
    window_date=${window_date%% *}
    delivery_key="${report_kind}-${window_date}"
  else
    delivery_key=$(basename -- "$report_path")
    delivery_key=${delivery_key%.md}
  fi
fi
if ! [[ "$delivery_key" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  log_error "Report key contains unsafe filename characters: $delivery_key"
  exit 1
fi

mkdir -p -- "$output_dir"
target_path="${output_dir}/${delivery_key}.md"
if [[ -e "$target_path" || -L "$target_path" ]]; then
  if [[ -L "$target_path" ]]; then
    log_error "Local report target is a symlink: $target_path"
    exit 1
  fi
  if [[ -f "$target_path" ]]; then
    printf 'Local report already exists; keeping authoritative file: %s\n' "$target_path"
    exit 0
  fi
  log_error "Local report target is not a regular file: $target_path"
  exit 1
fi

temporary_path=$(mktemp "${output_dir}/.${delivery_key}.tmp.XXXXXX")
cleanup() {
  rm -f -- "$temporary_path"
}
trap cleanup EXIT
cp -- "$report_path" "$temporary_path"

# ln 具备原子“仅当目标不存在才创建”语义；竞态下已有文件仍然优先保留。
if ln -- "$temporary_path" "$target_path" 2>/dev/null; then
  rm -f -- "$temporary_path"
  trap - EXIT
  printf 'Local report written: %s\n' "$target_path"
  exit 0
fi

if [[ -L "$target_path" ]]; then
  log_error "Local report target is a symlink: $target_path"
  exit 1
fi
if [[ -e "$target_path" ]]; then
  if [[ -f "$target_path" ]]; then
    printf 'Local report already exists; keeping authoritative file: %s\n' "$target_path"
    exit 0
  fi
  log_error "Local report target is not a regular file: $target_path"
  exit 1
fi

log_error "Unable to atomically create local report: $target_path"
exit 1
