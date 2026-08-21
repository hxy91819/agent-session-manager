#!/usr/bin/env bash
set -euo pipefail

# Validate a built asm binary through the public CLI with isolated provider stores.
binary=${1:?usage: verify-release-smoke.sh /path/to/asm}
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 2; }
test -x "$binary" || { echo "binary is not executable: $binary" >&2; exit 2; }

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
home="$root/dsh"
pi_home="$root/pi"
opencode_home="$root/opencode"
cwd="$root/project"
mkdir -p "$home/sessions/project/dsh-smoke" "$pi_home/sessions/--pi-smoke--" "$opencode_home" "$cwd"
printf '%s\n' \
  '{"type":"session","version":0,"id":"dsh-smoke","createdAt":1781312400000,"cwd":"'"$cwd"'","delegationDepth":0,"agentPreset":"standard"}' \
  '{"time":1781312460000,"type":"session/title","data":{"title":"release smoke","source":{"kind":"user"}}}' \
  '{"time":1781312520000,"type":"user/message","data":{"role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"verify release binary"}]}}' \
  > "$home/sessions/project/dsh-smoke/session.jsonl"

printf '%s\n' \
  '{"type":"session","version":3,"id":"pi-smoke","timestamp":"2026-06-13T01:00:00.000Z","cwd":"'"$cwd"'"}' \
  '{"type":"message","id":"pi-message","timestamp":"2026-06-13T01:01:00.000Z","message":{"role":"user","content":[{"type":"text","text":"verify Pi release binary"}]}}' \
  > "$pi_home/sessions/--pi-smoke--/2026-06-13T01-00-00-000Z_pi-smoke.jsonl"

python3 - "$opencode_home/opencode.db" "$cwd" <<'PY'
import sqlite3
import sys

db_path, cwd = sys.argv[1:]
db = sqlite3.connect(db_path)
db.executescript("""
CREATE TABLE session (
  id text primary key, project_id text not null, parent_id text,
  slug text not null, directory text not null, title text not null,
  version text not null, time_created integer not null,
  time_updated integer not null, time_archived integer
);
CREATE TABLE project (id text primary key, worktree text not null);
CREATE TABLE message (
  id text primary key, session_id text not null, time_created integer not null,
  time_updated integer not null, data text not null
);
CREATE TABLE part (
  id text primary key, message_id text not null, session_id text not null,
  time_created integer not null, time_updated integer not null, data text not null
);
""")
created = 1781312400000
db.execute("INSERT INTO project (id, worktree) VALUES (?, ?)", ("project-smoke", cwd))
db.execute("""INSERT INTO session
  (id, project_id, parent_id, slug, directory, title, version,
   time_created, time_updated, time_archived)
  VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, NULL)""",
  ("opencode-smoke", "project-smoke", "opencode-smoke", cwd,
   "New session - 2026-06-13T01:00:00.000Z", "1.18.18", created, created))
db.execute("INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)",
  ("message-opencode-smoke", "opencode-smoke", created, created,
   '{"role":"user","time":{"created":%d}}' % created))
db.execute("INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
  ("part-opencode-synthetic", "message-opencode-smoke", "opencode-smoke", created, created,
   '{"type":"text","text":"injected context","synthetic":true}'))
db.execute("INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)",
  ("part-opencode-user", "message-opencode-smoke", "opencode-smoke", created, created,
   '{"type":"text","text":"verify opencode release binary","synthetic":false}'))
db.commit()
db.close()
PY

empty_args=(
  --codex-home "$root/codex" --claude-home "$root/claude" --kimi-home "$root/kimi"
  --kiro-home "$root/kiro" --opencode-home "$opencode_home" --codebuddy-home "$root/codebuddy"
  --cursor-home "$root/cursor" --openclaw-home "$root/openclaw" --pi-home "$pi_home"
  --zcode-home "$root/zcode" --dsh-home "$home"
)

json=$("$binary" "${empty_args[@]}" --since-days 0 --limit 50 --json)
echo "$json" | jq -e '
  ([.sessions[] | select(.provider == "dsh" and .id == "dsh-smoke" and .title == "release smoke" and .metadata.title_source == "session_title")] | length == 1)
  and ([.sessions[] | select(.provider == "pi" and .id == "pi-smoke" and .title == "verify Pi release binary" and .metadata.title_source == "message")] | length == 1)
  and ([.sessions[] | select(.provider == "opencode" and .id == "opencode-smoke" and .title == "verify opencode release binary" and .metadata.title_source == "first_input" and .metadata.version == "1.18.18")] | length == 1)
' >/dev/null

for spec in \
  "dsh|dsh-smoke|'dsh' '--profile' 'tui' '--resume' 'dsh-smoke'" \
  "pi|pi-smoke|'pi' '--session' 'pi-smoke'" \
  "opencode|opencode-smoke|'opencode' '-s' 'opencode-smoke'"; do
  IFS='|' read -r provider id command_tail <<<"$spec"
  exec_out=$("$binary" resume "${empty_args[@]}" --provider "$provider" --since-days 0 --print-exec "$id")
  grep -F -- "$command_tail" <<<"$exec_out" >/dev/null
done

report=$("$binary" report "${empty_args[@]}" --start 2026-06-13 --end 2026-06-14 --limit 10)
echo "$report" | jq -e '
  .totals.sessions == 3
  and ([.sessions[] | select(.provider == "dsh" and (.evidence | length) > 0)] | length == 1)
  and ([.sessions[] | select(.provider == "pi" and (.evidence | length) > 0)] | length == 1)
  and ([.sessions[] | select(.provider == "opencode" and any(.evidence[]; .text == "verify opencode release binary"))] | length == 1)
' >/dev/null

missing_root="$root/missing"
mkdir -p "$home/sessions/project/dsh-missing"
sed "s#${cwd}#${missing_root}#; s#dsh-smoke#dsh-missing#g" "$home/sessions/project/dsh-smoke/session.jsonl" > "$home/sessions/project/dsh-missing/session.jsonl"
if "$binary" resume "${empty_args[@]}" --provider dsh --since-days 0 dsh-missing >/dev/null 2>&1; then
  echo "resume unexpectedly succeeded" >&2
  exit 1
fi

echo "release smoke passed: $binary"
