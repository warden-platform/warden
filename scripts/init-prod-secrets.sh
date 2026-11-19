#!/usr/bin/env bash
# 单机 Compose 生产形态的 Secret 一键初始化：在 .secrets/ 生成与
# deploy/docker-compose.prod.example.yaml 一一对应的 7 个文件。
#
# 用法：
#   make init-prod-secrets
#   scripts/init-prod-secrets.sh
#
# 可覆盖环境变量：
#   WARDEN_SECRETS_DIR  输出目录（默认 <仓库根>/.secrets，已 gitignore）
#   WARDEN_DB_HOST      DSN 主机（默认 postgresql，即 compose 服务名）
#   WARDEN_DB_PORT      DSN 端口（默认 5432，容器内端口）
#   WARDEN_DB_NAME      库名（默认 warden）
#   WARDEN_DB_USER      账号（默认 warden）
#
# 生成内容：4 个内部密钥（hex 32，两两不同）、postgres_password 与
# admin_password（hex 24）、用生成密码拼好的 warden_database_url。
# 全部 600 权限；任一目标文件已存在即整体拒绝，绝不覆盖。
# 不打印任何 Secret 值；admin_password 请自行存入密码管理器。
# OIDC client secret / AI API key 由外部服务提供，脚本不生成。
#
# 接入自有密钥管理系统（Vault/KMS）的部署可跳过本脚本，按同名文件自行准备。

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SECRETS_DIR="${WARDEN_SECRETS_DIR:-$ROOT_DIR/.secrets}"
DB_HOST="${WARDEN_DB_HOST:-postgresql}"
DB_PORT="${WARDEN_DB_PORT:-5432}"
DB_NAME="${WARDEN_DB_NAME:-warden}"
DB_USER="${WARDEN_DB_USER:-warden}"

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required to initialize production secrets" >&2
  exit 1
fi

FILES=(
  postgres_password
  warden_database_url
  warden_task_signing_key
  warden_jwt_signing_key
  warden_credential_encryption_key
  warden_admin_api_key
  warden_admin_password
)

existing=()
for f in "${FILES[@]}"; do
  [[ -e "$SECRETS_DIR/$f" ]] && existing+=("$f")
done
if [[ ${#existing[@]} -gt 0 ]]; then
  echo "refusing to overwrite existing secrets in $SECRETS_DIR:" >&2
  printf '  %s\n' "${existing[@]}" >&2
  exit 1
fi

umask 077
mkdir -p "$SECRETS_DIR"

PG_PASSWORD="$(openssl rand -hex 24)"
ADMIN_PASSWORD="$(openssl rand -hex 24)"

write_secret() {
  local name="$1" value="$2"
  local target="$SECRETS_DIR/$name" temporary="$SECRETS_DIR/$name.tmp.$$"
  printf '%s\n' "$value" > "$temporary"
  chmod 600 "$temporary"
  mv "$temporary" "$target"
}

write_secret postgres_password "$PG_PASSWORD"
write_secret warden_task_signing_key "$(openssl rand -hex 32)"
write_secret warden_jwt_signing_key "$(openssl rand -hex 32)"
write_secret warden_credential_encryption_key "$(openssl rand -hex 32)"
write_secret warden_admin_api_key "$(openssl rand -hex 32)"
write_secret warden_admin_password "$ADMIN_PASSWORD"
# 密码为 hex（无 URL 特殊字符），可直接拼装；外部 TLS 数据库请自行改 sslmode。
write_secret warden_database_url "postgres://$DB_USER:$PG_PASSWORD@$DB_HOST:$DB_PORT/$DB_NAME?sslmode=disable"

echo "initialized production secrets in $SECRETS_DIR (mode 600):"
printf '  %s\n' "${FILES[@]}"
echo
echo "下一步："
echo "  1. 把 $SECRETS_DIR/warden_admin_password 存入密码管理器（登录 admin 用，脚本不回显）。"
echo "  2. 如需 OIDC/AI，另行准备 oidc_client_secret / ai_api_key 并在 compose 取消注释。"
echo "  3. 按 docs/guides.md#installation 继续：迁移 → docker compose up。"