#!/usr/bin/env bash
# 单机 Docker Compose 部署/回滚：切换 WARDEN_VERSION → 拉镜像 → 重启 → 探活，
# 任一步失败自动切回上一个版本。供 GitHub Actions（自托管 runner）与人工执行共用。
#
# 用法：
#   ./scripts/deploy-compose.sh v0.4.3
#   DEPLOY_DIR=/opt/warden HEALTH_URL=http://127.0.0.1:8080/api/v1/health ./scripts/deploy-compose.sh v0.4.3
set -euo pipefail

VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "用法: $0 <version，如 v0.4.3>" >&2
  exit 2
fi

DEPLOY_DIR="${DEPLOY_DIR:-/opt/warden}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yaml}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
ENV_FILE="$DEPLOY_DIR/.env"

cd "$DEPLOY_DIR"

if [ ! -f "$COMPOSE_FILE" ]; then
  echo "未找到编排文件: $DEPLOY_DIR/$COMPOSE_FILE（可用 COMPOSE_FILE 覆盖）" >&2
  exit 2
fi

PREV_VERSION="$(sed -n 's/^WARDEN_VERSION=//p' "$ENV_FILE" 2>/dev/null | tail -1 || true)"

set_version() {
  local v="$1" tmp
  touch "$ENV_FILE"
  if grep -qE '^WARDEN_VERSION=' "$ENV_FILE"; then
    tmp="$(mktemp)"
    sed "s|^WARDEN_VERSION=.*|WARDEN_VERSION=$v|" "$ENV_FILE" >"$tmp"
    mv "$tmp" "$ENV_FILE"
  else
    printf 'WARDEN_VERSION=%s\n' "$v" >>"$ENV_FILE"
  fi
}

wait_healthy() {
  local i
  for i in $(seq 1 30); do
    if curl -fsS --max-time 3 "$HEALTH_URL" >/dev/null 2>&1; then
      echo "健康检查通过（${i}×2s）: $HEALTH_URL"
      return 0
    fi
    sleep 2
  done
  return 1
}

rollback() {
  if [ -z "$PREV_VERSION" ]; then
    echo "无上一个版本可回滚（首次部署），保持当前状态并失败退出" >&2
    return 1
  fi
  echo "回滚到 $PREV_VERSION ..." >&2
  set_version "$PREV_VERSION"
  docker compose -f "$COMPOSE_FILE" pull
  docker compose -f "$COMPOSE_FILE" up -d --remove-orphans
  echo "已回滚到 $PREV_VERSION" >&2
}

echo "部署 $VERSION（上一个版本: ${PREV_VERSION:-无}）"
set_version "$VERSION"

if ! docker compose -f "$COMPOSE_FILE" pull; then
  echo "镜像拉取失败，回滚" >&2
  rollback || true
  exit 1
fi

if ! docker compose -f "$COMPOSE_FILE" up -d --remove-orphans; then
  echo "容器拉起失败，回滚" >&2
  rollback || true
  exit 1
fi

if ! wait_healthy; then
  echo "健康检查失败，回滚" >&2
  rollback || true
  exit 1
fi

echo "部署完成: $VERSION"
