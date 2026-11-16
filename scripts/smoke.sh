#!/usr/bin/env bash
# 端到端冒烟测试：初始化 -> 建目标库 -> 建策略 -> 连跑多次备份 -> 校验保留策略与归档有效性
# 用法：BASE=http://127.0.0.1:8080 ./scripts/smoke.sh
set -euo pipefail

BASE=${BASE:-http://127.0.0.1:8080}
PGHOST=${PGHOST:-dbb-pg}
PGPORT=${PGPORT:-5432}
PGDB=${PGDB:-appdb}
PGUSER=${PGUSER:-backup_ro}
PGPASS=${PGPASS:-backup_pass}
RUNS=${RUNS:-5}
PY=${PY:-python}

JAR=$(mktemp)
OUT=$(mktemp)
trap 'rm -f "$JAR" "$OUT"' EXIT

say() { printf '\n=== %s ===\n' "$1"; }

say "bootstrap"
curl -s "$BASE/api/system/bootstrap"; echo

say "setup admin"
curl -s -c "$JAR" -X POST -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"Admin12345"}' "$BASE/api/auth/setup"; echo

say "create target"
TARGET_ID=$(curl -s -b "$JAR" -X POST -H 'Content-Type: application/json' \
  -d "{\"name\":\"appdb-test\",\"host\":\"$PGHOST\",\"port\":$PGPORT,\"dbname\":\"$PGDB\",\"username\":\"$PGUSER\",\"password\":\"$PGPASS\",\"ssl_mode\":\"disable\",\"tags\":[\"smoke\"]}" \
  "$BASE/api/targets" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); print(d.get("id", "ERR"))')
echo "target_id=$TARGET_ID"
if [ "$TARGET_ID" = "ERR" ]; then echo "创建目标库失败"; exit 1; fi

say "create policy (retain 3 copies / 7 days, keep min 2)"
curl -s -b "$JAR" -X POST -H 'Content-Type: application/json' \
  -d "{\"target_id\":$TARGET_ID,\"cron_expr\":\"0 2 * * *\",\"timezone\":\"Asia/Shanghai\",\"retain_count\":3,\"retain_days\":7,\"keep_min_count\":2,\"verify_mode\":\"toc\",\"compression\":\"zstd:5\",\"encrypt\":true,\"timeout_seconds\":600}" \
  "$BASE/api/policies" | "$PY" -c 'import json,sys
d=json.load(sys.stdin)
print("policy_id=%s next_run=%s" % (d.get("id"), d.get("next_run_at")))'

say "trigger $RUNS backups"
for _ in $(seq 1 "$RUNS"); do
  curl -s -b "$JAR" -X POST "$BASE/api/targets/$TARGET_ID/backup" > /dev/null
  sleep 2
done

sleep 3
say "jobs"
curl -s -b "$JAR" "$BASE/api/jobs?limit=20" > "$OUT"
"$PY" - "$OUT" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print("total:", d["total"])
for j in d["items"]:
    print("  #%-3s %-8s %8sB  client=%-10s %sms" % (
        j["id"], j["status"], j["size_bytes"], j["tool_version"], j["duration_ms"]))
PY

JOB_ID=$("$PY" -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d["items"][0]["id"])' "$OUT")
say "artifacts of job #$JOB_ID"
curl -s -b "$JAR" "$BASE/api/jobs/$JOB_ID/artifacts" > "$OUT"
"$PY" - "$OUT" <<'PY'
import json, sys
for a in json.load(open(sys.argv[1])):
    print("  artifact #%s %s %sB encrypted=%s" % (a["id"], a["object_key"], a["size_bytes"], a["encrypted"]))
PY

say "download + archive check"
ART_ID=$("$PY" -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d[0]["id"])' "$OUT")
curl -s -b "$JAR" -o /tmp/smoke.dump "$BASE/api/artifacts/$ART_ID/download"
ls -l /tmp/smoke.dump
if command -v pg_restore >/dev/null 2>&1; then
  pg_restore --list /tmp/smoke.dump | head -6
  echo "toc_lines=$(pg_restore --list /tmp/smoke.dump | wc -l)"
else
  echo "pg_restore 不在 PATH，跳过归档校验（可在容器内执行）"
fi

say "stats"
curl -s -b "$JAR" "$BASE/api/stats"; echo

say "audit (last 5)"
curl -s -b "$JAR" "$BASE/api/audit?limit=5" > "$OUT"
"$PY" - "$OUT" <<'PY'
import json, sys
for a in json.load(open(sys.argv[1])):
    print("  %s %-20s %-16s %s" % (a["created_at"][:19], a["actor"], a["action"], a["object_ref"]))
PY

printf '\n冒烟测试完成。\n'
