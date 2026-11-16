#!/usr/bin/env bash
# 一站式回归验证。约束：runas/trustlevel 启动的 PG 随本会话结束而终止，
# 因此所有验证必须在同一会话内完成，结束时主动清理。
set -u
ROOT="E:/opt/Qoder/db-backups"
LOCAL="$ROOT/.local"
PGHOME="E:/code_repo/argus/tools/pgsql/pgsql17/bin"
PGHOME15="E:/code_repo/argus/tools/pgsql/pgsql/bin"
PSQL="$PGHOME/psql.exe"
READY="$PGHOME/pg_isready.exe"
BASE="http://127.0.0.1:8080"
JAR="$LOCAL/cookies_verify.txt"
PY="python"

say() { printf '\n===== %s =====\n' "$1"; }

cleanup() {
  printf '\n===== cleanup =====\n'
  taskkill //F //IM backupd.exe >/dev/null 2>&1
  taskkill //F //IM postgres.exe >/dev/null 2>&1
  rm -f "$JAR"
}
trap cleanup EXIT

rm -f "$JAR" "$LOCAL/server.log" "$LOCAL/pg17.log"
rm -rf "$LOCAL/pg17data"
"$LOCAL/go64/go/bin/go.exe" version >/dev/null 2>&1 || true

say "1. initdb + start PG17 (trustlevel)"
rm -f "$LOCAL/initdb.log"
cmd //c "runas /trustlevel:0x20000 E:\\opt\\Qoder\\db-backups\\.local\\init.bat" >/dev/null 2>&1
for _ in $(seq 1 30); do
  [ -f "$LOCAL/pg17data/PG_VERSION" ] && break
  sleep 2
done
[ -f "$LOCAL/pg17data/PG_VERSION" ] || { echo "initdb FAILED"; tail -5 "$LOCAL/initdb.log"; exit 1; }
echo "initdb ok"

cmd //c "runas /trustlevel:0x20000 E:\\opt\\Qoder\\db-backups\\.local\\start.bat" >/dev/null 2>&1
ok=""
for _ in $(seq 1 20); do
  if "$READY" -h 127.0.0.1 -p 55432 2>/dev/null | grep -q accepting; then ok=1; break; fi
  sleep 2
done
[ -n "$ok" ] || { echo "pg start FAILED"; tail -5 "$LOCAL/pg17.log"; exit 1; }
echo "pg17 ready on 55432"

say "2. seed metadata db + target db"
"$PSQL" -h 127.0.0.1 -p 55432 -U postgres -q <<'SQL' || exit 1
CREATE DATABASE dbbackups;
CREATE DATABASE appdb;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='backup_ro') THEN
    CREATE ROLE backup_ro LOGIN PASSWORD 'backup_pass';
  END IF;
END $$;
GRANT pg_read_all_data TO backup_ro;
SQL
"$PSQL" -h 127.0.0.1 -p 55432 -U postgres -q -d appdb <<'SQL' || exit 1
CREATE TABLE customers (id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL, city TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE orders (id BIGSERIAL PRIMARY KEY, customer_id BIGINT NOT NULL REFERENCES customers(id), amount NUMERIC(12,2) NOT NULL, status TEXT NOT NULL DEFAULT 'pending', created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX orders_status_idx ON orders (status);
INSERT INTO customers (email, city) SELECT 'user_'||i||'@example.com', (ARRAY['cz','nj','sz'])[1+i%3] FROM generate_series(1,2000) i;
INSERT INTO orders (customer_id, amount, status) SELECT 1+(i%2000), (i%500)::numeric/7, (ARRAY['pending','paid','shipped','refunded'])[1+i%4] FROM generate_series(1,20000) i;
SQL
echo "seeded"

say "3. start backupd (local, pg clients from argus tools)"
(
  export BACKUP_DATABASE_URL="postgres://postgres@127.0.0.1:55432/dbbackups?sslmode=disable"
  export BACKUP_DATA_DIR="$LOCAL/data"
  export BACKUP_PG_BIN_DIRS="$PGHOME15,$PGHOME"
  export BACKUP_HTTP_ADDR=":8080"
  exec "$LOCAL/backupd.exe"
) > "$LOCAL/server.log" 2>&1 &
SRV_PID=$!
for _ in $(seq 1 20); do
  curl -s --max-time 3 "$BASE/api/system/bootstrap" >/dev/null 2>&1 && break
  sleep 1
done
curl -s --max-time 5 "$BASE/api/system/bootstrap" || { echo "server FAILED"; tail -10 "$LOCAL/server.log"; exit 1; }
echo
grep -c "发现 pg 客户端" "$LOCAL/server.log" | xargs echo "pg clients found:"

say "4. smoke (5 backups via API)"
cd "$ROOT" && BASE="$BASE" PGHOST=127.0.0.1 PGPORT=55432 PGDB=appdb PGUSER=backup_ro PGPASS=backup_pass RUNS=5 bash scripts/smoke.sh > "$LOCAL/smoke.out" 2>&1
tail -30 "$LOCAL/smoke.out"

say "5. stored objects count (expect 3)"
docker_used="no"
if command -v docker >/dev/null 2>&1; then :; fi
echo "objects: $(find "$LOCAL/data/objects" -type f 2>/dev/null | wc -l)"
"$PSQL" -h 127.0.0.1 -p 55432 -U postgres -q -d dbbackups -t -c "select count(*) from artifacts where not is_deleted" | xargs echo "live artifacts:"

say "6. SSE log stream (job 1, first lines)"
curl -s -c "$JAR" -X POST -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"Admin12345"}' "$BASE/api/auth/login" > /dev/null
(curl -s -N --max-time 4 -b "$JAR" "$BASE/api/jobs/1/logs/stream" || true) | head -8

say "7. auth guard + bad password guard"
curl -s -o /dev/null --max-time 5 -w "no-cookie /api/targets -> %{http_code}\n" "$BASE/api/targets"
curl -s -b "$JAR" --max-time 15 -X POST -H 'Content-Type: application/json' \
  -d '{"name":"bad-conn","host":"127.0.0.1","port":55432,"dbname":"appdb","username":"backup_ro","password":"wrong","ssl_mode":"disable"}' \
  "$BASE/api/targets"; echo

say "8. scheduler real-fire test (cron every minute)"
curl -s -b "$JAR" --max-time 10 -X DELETE "$BASE/api/policies/1" > /dev/null
curl -s -b "$JAR" --max-time 10 -X POST -H 'Content-Type: application/json' \
  -d '{"target_id":1,"cron_expr":"* * * * *","timezone":"Asia/Shanghai","retain_count":10,"retain_days":0,"keep_min_count":2,"verify_mode":"toc","compression":"zstd:5","encrypt":true,"timeout_seconds":600}' \
  "$BASE/api/policies" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); print("policy_id=%s next_run=%s" % (d.get("id"), d.get("next_run_at")))'
echo "waiting 75s for cron fire..."
sleep 75
curl -s -b "$JAR" --max-time 10 "$BASE/api/jobs?limit=20" | "$PY" -c '
import json,sys
d=json.load(sys.stdin)
sched=[j for j in d["items"] if j["trigger"]=="schedule"]
print("total jobs:", d["total"], "| schedule-triggered:", len(sched))
for j in sched[:3]:
    print("  #%s %s %sB" % (j["id"], j["status"], j["size_bytes"]))
'
say "9. final audit tail"
curl -s -b "$JAR" --max-time 10 "$BASE/api/audit?limit=8" | "$PY" -c '
import json,sys
for a in json.load(sys.stdin):
    print("  %s %-20s %s" % (a["created_at"][11:19], a["action"], a["object_ref"]))
'
echo
echo "VERIFY_DONE"
