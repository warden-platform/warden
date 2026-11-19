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
# A complete DATABASE_URL (with credentials) is used as-is; local_config's
# credential injection would otherwise mangle DSNs that already carry auth.
DB_URL="${DATABASE_URL:-$(python3 -c "
import sys
sys.path.insert(0, '$ROOT_DIR/scripts/demo')
from local_config import database_url
print(database_url())
")}"
export PGPASSWORD=$(DB_URL="$DB_URL" python3 -c "import os;from urllib.parse import urlparse;print(urlparse(os.environ['DB_URL']).password or '')")
HOST=$(DB_URL="$DB_URL" python3 -c "import os;from urllib.parse import urlparse;print(urlparse(os.environ['DB_URL']).hostname or 'localhost')")
PORT=$(DB_URL="$DB_URL" python3 -c "import os;from urllib.parse import urlparse;print(urlparse(os.environ['DB_URL']).port or 5432)")
USER=$(DB_URL="$DB_URL" python3 -c "import os;from urllib.parse import urlparse;print(urlparse(os.environ['DB_URL']).username)")
DB=$(DB_URL="$DB_URL" python3 -c "import os;from urllib.parse import urlparse;print(urlparse(os.environ['DB_URL']).path.lstrip('/'))")
while true; do
  "$PSQL" -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -q -c \
    "UPDATE connectors SET last_heartbeat=now(), updated_at=now() WHERE tenant_id='acme' AND name <> 'h3c-core-sw' AND archived_at IS NULL;" \
    >/dev/null 2>&1 || echo "heartbeat update failed (is the DB up?)" >&2
  sleep 30
done
