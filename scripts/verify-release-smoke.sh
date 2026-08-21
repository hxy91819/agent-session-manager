#!/usr/bin/env bash
set -euo pipefail

# Validate a built asm binary through the public CLI with isolated provider stores.
binary=${1:?usage: verify-release-smoke.sh /path/to/asm}
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
test -x "$binary" || { echo "binary is not executable: $binary" >&2; exit 2; }

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
home="$root/dsh"
cwd="$root/project"
mkdir -p "$home/sessions/project/smoke" "$cwd"
printf '%s\n' \
  '{"type":"session","version":0,"id":"smoke","createdAt":1781312400000,"cwd":"'"$cwd"'","delegationDepth":0,"agentPreset":"standard"}' \
  '{"time":1781312460000,"type":"session/title","data":{"title":"release smoke","source":{"kind":"user"}}}' \
  '{"time":1781312520000,"type":"user/message","data":{"role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"verify release binary"}]}}' \
  > "$home/sessions/project/smoke/session.jsonl"

empty_args=(
  --codex-home "$root/codex" --claude-home "$root/claude" --kimi-home "$root/kimi"
  --kiro-home "$root/kiro" --opencode-home "$root/opencode" --codebuddy-home "$root/codebuddy"
  --cursor-home "$root/cursor" --openclaw-home "$root/openclaw" --pi-home "$root/pi"
  --zcode-home "$root/zcode" --dsh-home "$home"
)

json=$("$binary" "${empty_args[@]}" --since-days 0 --json --query release)
echo "$json" | jq -e '.sessions | length == 1 and .[0].provider == "dsh" and .[0].id == "smoke" and .[0].title == "release smoke"' >/dev/null

exec_out=$("$binary" "${empty_args[@]}" --since-days 0 --resume smoke --print-exec)
grep -F "dsh" <<<"$exec_out" >/dev/null
grep -F -- "--resume' 'smoke" <<<"$exec_out" >/dev/null

missing_root="$root/missing"
sed "s#${cwd}#${missing_root}#; s#\"smoke\"#\"missing\"#g" "$home/sessions/project/smoke/session.jsonl" > "$home/sessions/project/smoke/missing.jsonl"
if "$binary" resume "${empty_args[@]}" --provider dsh --since-days 0 missing >/dev/null 2>&1; then
  echo "resume unexpectedly succeeded" >&2
  exit 1
fi

echo "release smoke passed: $binary"
