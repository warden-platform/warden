#!/usr/bin/env bash
# 生产部署：切换控制面镜像版本 + 发布前端静态产物，任一步失败自动回滚。
#
# 该文件同时是部署机 /usr/local/bin/warden-deploy 的源文件（sudoers 只放行这个
# root:root 路径，因此 runner 不能直接执行工作区里的脚本）：
#   sudo install -m 755 scripts/deploy-compose.sh /usr/local/bin/warden-deploy
#   echo 'runner ALL=(root) NOPASSWD: /usr/local/bin/warden-deploy' \
#     | sudo tee /etc/sudoers.d/warden-deploy && sudo visudo -cf /etc/sudoers.d/warden-deploy
#
# 用法：
#   ./scripts/deploy-compose.sh v0.4.3
#   DEPLOY_DIR=/opt/warden HEALTH_URL=http://127.0.0.1:8080/api/v1/health ./scripts/deploy-compose.sh v0.4.3
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# 部署机可在 /etc/warden/deploy.env 里固化本机路径（root:root 600），脚本本身无需改动。
DEPLOY_ENV_FILE="${DEPLOY_ENV_FILE:-/etc/warden/deploy.env}"
if [ -r "$DEPLOY_ENV_FILE" ]; then
  # shellcheck disable=SC1090
  . "$DEPLOY_ENV_FILE"
fi

REGISTRY="${REGISTRY:-registry.cn-hangzhou.aliyuncs.com/warden-stable}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/warden}"
COMPOSE_FILE="${COMPOSE_FILE:-$DEPLOY_DIR/docker-compose.yaml}"
ENV_FILE="${ENV_FILE:-$DEPLOY_DIR/.env}"
WEB_ROOT="${WEB_ROOT:-}"
BACKUP_DIR="${BACKUP_DIR:-$DEPLOY_DIR/backups}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
KEEP_BACKUPS="${KEEP_BACKUPS:-10}"
LOG_FILE="${LOG_FILE:-/var/log/warden-deploy.log}"

if [ -w "$(dirname "$LOG_FILE" 2>/dev/null || echo /)" ] 2>/dev/null; then
  exec > >(tee -a "$LOG_FILE") 2>&1
fi

VERSION="${1:-}"
case "$VERSION" in
  v[0-9]*) ;;
  *) echo "用法: $0 <version，如 v0.4.3>" >&2; exit 2 ;;
esac
if [[ ! "$VERSION" =~ ^v[0-9][0-9A-Za-z._-]*$ ]]; then
  echo "版本号含非法字符: $VERSION" >&2
  exit 2
fi

cd "$DEPLOY_DIR"
if [ ! -f "$COMPOSE_FILE" ]; then
  echo "未找到编排文件: $COMPOSE_FILE" >&2
  exit 2
fi

echo "===== $(date '+%F %T') 部署 $VERSION ====="

PREV_VERSION="$(sed -n 's/^WARDEN_VERSION=//p' "$ENV_FILE" 2>/dev/null | tail -1 || true)"
if [ -z "$PREV_VERSION" ]; then
  PREV_VERSION="$(docker inspect -f '{{.Config.Image}}' deploy-warden-control-1 2>/dev/null | sed 's/.*://' || true)"
fi

set_version() {
  local v="$1" tmp
  touch "$ENV_FILE"
  chmod 600 "$ENV_FILE"
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

rollback_control() {
  if [ -z "$PREV_VERSION" ]; then
    echo "无上一个版本可回滚（首次部署），保持当前状态并失败退出" >&2
    return 1
  fi
  echo "回滚控制面到 $PREV_VERSION ..." >&2
  set_version "$PREV_VERSION"
  docker compose -f "$COMPOSE_FILE" pull
  docker compose -f "$COMPOSE_FILE" up -d --remove-orphans
}

# 前端是宿主机静态目录（外层 nginx 直接读），从 warden-web 镜像里取产物覆盖。
deploy_web() {
  local ver="$1" tmp unit backup
  tmp="$(mktemp -d)"
  unit="warden-web-extract"
  mkdir -p "$BACKUP_DIR"
  backup="$BACKUP_DIR/dist-${PREV_VERSION:-unknown}-$(date +%Y%m%d-%H%M%S).tar.gz"
  tar czf "$backup" -C "$(dirname "$WEB_ROOT")" "$(basename "$WEB_ROOT")"
  echo "前端已备份: $backup"

  docker rm -f "$unit" >/dev/null 2>&1 || true
  docker create --name "$unit" "$REGISTRY/warden-web:$ver" >/dev/null
  docker cp "$unit:/usr/share/nginx/html/." "$tmp"/
  docker rm -f "$unit" >/dev/null

  if [ ! -f "$tmp/index.html" ]; then
    rm -rf "$tmp"
    echo "镜像内未找到前端产物" >&2
    return 1
  fi

  if ! rsync -a --delete "$tmp"/ "$WEB_ROOT"/; then
    rm -rf "$tmp"
    echo "同步前端失败，回滚静态文件" >&2
    rm -rf "$WEB_ROOT"
    tar xzf "$backup" -C "$(dirname "$WEB_ROOT")"
    return 1
  fi
  rm -rf "$tmp"
  echo "前端已更新: $WEB_ROOT（$ver）"

  ls -1t "$BACKUP_DIR"/dist-*.tar.gz 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f
}

echo "上一个版本: ${PREV_VERSION:-无}"

if ! docker compose -f "$COMPOSE_FILE" pull warden-control; then
  echo "控制面镜像拉取失败" >&2
  exit 1
fi
if [ -n "$WEB_ROOT" ] && ! docker pull "$REGISTRY/warden-web:$VERSION"; then
  echo "前端镜像拉取失败" >&2
  exit 1
fi

set_version "$VERSION"

if ! docker compose -f "$COMPOSE_FILE" up -d --remove-orphans; then
  echo "容器拉起失败，回滚" >&2
  rollback_control || true
  exit 1
fi

if ! wait_healthy; then
  echo "健康检查失败，回滚" >&2
  rollback_control || true
  exit 1
fi

if [ -n "$WEB_ROOT" ]; then
  if ! deploy_web "$VERSION"; then
    echo "前端发布失败" >&2
    exit 1
  fi
fi

echo "===== 部署完成: $VERSION ====="
