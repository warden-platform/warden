#!/usr/bin/env bash
# 建应用数据库角色并注入密码（RLS 地基，配合 deploy/db/roles.sql）。
# 以 superuser（warden）执行 roles.sql 建角色+授权，再从 .secrets 读取
# warden_app_password / warden_privileged_password 注入并启用登录。
# 密码经 here-doc 进 psql stdin，不进命令行参数、不回显。
#
# 用法：make init-db-roles（先 make init-prod-secrets 生成密码、起库）
# 可覆盖：COMPOSE / PG_SERVICE / PG_USER / PG_DB / WARDEN_SECRETS_DIR
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SECRETS_DIR="${WARDEN_SECRETS_DIR:-$ROOT_DIR/.secrets}"
COMPOSE="${COMPOSE:-docker compose --env-file .env -f deploy/docker-compose.yaml}"
PG_SERVICE="${PG_SERVICE:-postgresql}"
PG_USER="${PG_USER:-warden}"
PG_DB="${PG_DB:-warden}"

for f in warden_app_password warden_privileged_password; do
  if [[ ! -r "$SECRETS_DIR/$f" ]]; then
    echo "missing $SECRETS_DIR/$f（先运行 make init-prod-secrets）" >&2
    exit 1
  fi
done

psql_exec() { $COMPOSE exec -T "$PG_SERVICE" psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB"; }

echo "==> 建角色与授权（deploy/db/roles.sql）"
psql_exec >/dev/null < "$ROOT_DIR/deploy/db/roles.sql"

app_pw=$(cat "$SECRETS_DIR/warden_app_password")
priv_pw=$(cat "$SECRETS_DIR/warden_privileged_password")
echo "==> 注入角色密码并启用登录"
psql_exec >/dev/null <<SQL
ALTER ROLE warden_app LOGIN PASSWORD '$app_pw';
ALTER ROLE warden_privileged LOGIN PASSWORD '$priv_pw';
SQL

echo "完成：warden_app（运行时，受 RLS 约束）/ warden_privileged（旁路，BYPASSRLS）已就绪。"