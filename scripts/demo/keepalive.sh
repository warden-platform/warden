#!/usr/bin/env bash
# Demo heartbeat keepalive: refreshes the five "online" acme demo connectors
# every 30s so the console shows live heartbeats. h3c-core-sw stays offline
# on purpose (demonstrates the degraded state). Stop with: kill <pid>.
set -euo pipefail
ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PSQL=$(command -v psql || true)
if [[ -z "$PSQL" ]]; then
  for c in /opt/homebrew/opt/postgresql@18/bin/psql /opt/homebrew/opt/postgresql@17/bin/psql; do
    [[ -x "$c" ]] && PSQL="$c" && break
  done
fi
[[ -n "$PSQL" ]] || { echo "psql not found" >&2; exit 1; }
DB_URL=$(cat "$ROOT_DIR/.secrets/database_url")
export PGPASSWORD=$(python3 -c "from urllib.parse import urlparse;print(urlparse('''$DB_URL''').password or '')")
HOST=$(python3 -c "from urllib.parse import urlparse;print(urlparse('''$DB_URL''').hostname or 'localhost')")
PORT=$(python3 -c "from urllib.parse import urlparse;print(urlparse('''$DB_URL''').port or 5432)")
USER=$(python3 -c "from urllib.parse import urlparse;print(urlparse('''$DB_URL''').username)")
DB=$(python3 -c "from urllib.parse import urlparse;print(urlparse('''$DB_URL''').path.lstrip('/'))")
while true; do
  "$PSQL" -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -q -c \
    "UPDATE connectors SET last_heartbeat=now(), updated_at=now() WHERE tenant_id='acme' AND name <> 'h3c-core-sw' AND archived_at IS NULL;" \
    >/dev/null 2>&1 || echo "heartbeat update failed (is the DB up?)" >&2
  sleep 30
done