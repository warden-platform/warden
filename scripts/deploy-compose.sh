#!/usr/bin/env bash
# 生产部署：备份数据库 → 应用新增迁移 → 切换控制面镜像 + 发布前端静态产物，
# 任一步失败自动回滚（数据库迁移不会自动回退，失败时保留备份供人工恢复）。
#
# 该文件同时是部署机 /usr/local/bin/warden-deploy 的源文件（sudoers 只放行这个
# root:root 路径，因此 runner 不能直接执行工作区里的脚本）：
#   sudo install -m 755 scripts/deploy-compose.sh /usr/local/bin/warden-deploy
#   echo 'runner ALL=(root) NOPASSWD: /usr/local/bin/warden-deploy' \
#     | sudo tee /etc/sudoers.d/warden-deploy && sudo visudo -cf /etc/sudoers.d/warden-deploy
# 修改本脚本后必须重新 install 一次，Actions 才会使用新版本。
#
# 用法：
#   ./scripts/deploy-compose.sh v0.4.3
#   DEPLOY_DIR=/opt/warden HEALTH_URL=http://127.0.0.1:8080/api/v1/health ./scripts/deploy-compose.sh v0.4.3
#
# 迁移策略（可在 /etc/warden/deploy.env 覆盖）：
#   MIGRATION_MODE=auto  只应用「新版本发布清单 − 上一版本发布清单」的差量（默认）
#                        默认假定已发布迁移文件不再修改；若修正了已发布迁移，用 all 重放
#   MIGRATION_MODE=all   重放新版本清单中的全部迁移（清单内迁移必须幂等；注意
#                        106 会无条件撤销全部会话，触发全员重登）
#   MIGRATION_MODE=skip  不执行迁移（数据库由外部系统托管时使用）
#   PG_SERVICE / PG_USER / PG_DB  迁移目标，默认 postgresql / warden / warden
#                                （PG_USER 必须是表 owner/迁移角色，不能是受 RLS 约束的 warden_app）
#   SKIP_DB_BACKUP=1     跳过迁移前的自动 pg_dump -Fc（默认会备份）
#   KEEP_DB_BACKUPS      保留最近几份数据库备份，默认 5
# 迁移文件与清单直接从新版本控制面镜像的 /app-artifacts 读取，不依赖宿主机仓库。
#
# 脚本自更新：控制面镜像内 /app-artifacts/scripts/deploy-compose.sh 是本脚本的发布副本。
# 通过 /usr/local/bin/warden-deploy 以 root 调用时，拉取新版本镜像后会校验并原子替换
# 自身，再重载执行，使后续脚本变更不再需要人工 SSH 安装。覆盖前会把当前脚本备份到
# $BACKUP_DIR/bin/ 并保留最近 KEEP_SCRIPT_BACKUPS 份。首次引入自更新逻辑仍需人工安装
# 一次引导版本；非安装路径调用（如直接跑仓库脚本）不会覆盖宿主脚本。
# 禁用自更新：WARDEN_DEPLOY_SELF_UPDATE=0。
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

# 部署机可在 /etc/warden/deploy.env 里固化本机路径（root:root 600），脚本本身无需改动。
DEPLOY_ENV_FILE="${DEPLOY_ENV_FILE:-/etc/warden/deploy.env}"
if [ -r "$DEPLOY_ENV_FILE" ]; then
  # shellcheck disable=SC1090
  . "$DEPLOY_ENV_FILE"
fi

REGISTRY="${REGISTRY:-registry.cn-hangzhou.aliyuncs.com/warden-stable}"
CONTROL_IMAGE="${CONTROL_IMAGE:-$REGISTRY/warden}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/warden}"
COMPOSE_FILE="${COMPOSE_FILE:-$DEPLOY_DIR/docker-compose.yaml}"
ENV_FILE="${ENV_FILE:-$DEPLOY_DIR/.env}"
WEB_ROOT="${WEB_ROOT:-}"
BACKUP_DIR="${BACKUP_DIR:-$DEPLOY_DIR/backups}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
KEEP_BACKUPS="${KEEP_BACKUPS:-10}"
KEEP_DB_BACKUPS="${KEEP_DB_BACKUPS:-5}"
PG_SERVICE="${PG_SERVICE:-postgresql}"
PG_USER="${PG_USER:-warden}"
PG_DB="${PG_DB:-warden}"
MIGRATION_MODE="${MIGRATION_MODE:-auto}"
SKIP_DB_BACKUP="${SKIP_DB_BACKUP:-0}"
SELF_TARGET="${WARDEN_DEPLOY_SELF_PATH:-/usr/local/bin/warden-deploy}"
SELF_BACKUP_DIR="${SELF_BACKUP_DIR:-$BACKUP_DIR/bin}"
KEEP_SCRIPT_BACKUPS="${KEEP_SCRIPT_BACKUPS:-5}"
LOG_FILE="${LOG_FILE:-/var/log/warden-deploy.log}"

# 自更新会 exec 新脚本并继承旧脚本的 tee；此时不要重复套一层，否则日志双写。
if [ "${WARDEN_DEPLOY_SELF_UPDATED:-0}" != "1" ] &&
   [ -w "$(dirname "$LOG_FILE" 2>/dev/null || echo /)" ] 2>/dev/null; then
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

case "$MIGRATION_MODE" in
  auto|all|skip) ;;
  *) echo "无效 MIGRATION_MODE: $MIGRATION_MODE（可选 auto/all/skip）" >&2; exit 2 ;;
esac

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
  if [ "$MIGRATIONS_APPLIED" -gt 0 ]; then
    echo "注意：本次已应用 $MIGRATIONS_APPLIED 个数据库迁移；回滚只切回镜像，不回退 schema，备份见 $BACKUP_DIR" >&2
  fi
  echo "回滚控制面到 $PREV_VERSION ..." >&2
  set_version "$PREV_VERSION"
  docker compose -f "$COMPOSE_FILE" pull
  docker compose -f "$COMPOSE_FILE" up -d --remove-orphans
}

# --- 数据库迁移 ---------------------------------------------------------
# 控制面启动不跑迁移，Readiness 也只 ping 数据库；漏迁移不会在启动时报错，
# 而是接口运行到缺失 schema 时才 500。因此部署脚本在切换版本前显式应用迁移。

WORK_DIR=""
EXTRACT_UNIT=""
MIGRATIONS_APPLIED=0

cleanup_workdir() {
  if [ -n "$WORK_DIR" ]; then
    rm -rf "$WORK_DIR"
    WORK_DIR=""
  fi
  if [ -n "$EXTRACT_UNIT" ]; then
    docker rm -f "$EXTRACT_UNIT" >/dev/null 2>&1 || true
    EXTRACT_UNIT=""
  fi
}
trap cleanup_workdir EXIT

compose_exec() { docker compose -f "$COMPOSE_FILE" "$@"; }

psql_exec() {
  compose_exec exec -T "$PG_SERVICE" psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" "$@"
}

postgres_running() {
  [ -n "$(compose_exec ps -q "$PG_SERVICE" 2>/dev/null)" ]
}

# 从控制面镜像读取该版本的发布迁移清单与 SQL（镜像内 /app-artifacts 自带）。
extract_release_artifacts() {
  local ver="$1" out="$2" image unit ok=1
  image="$CONTROL_IMAGE:$ver"
  unit="warden-artifacts-$$-$RANDOM"
  mkdir -p "$out/sql"
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "拉取镜像以读取迁移清单: $image" >&2
    docker pull "$image" >/dev/null || return 1
  fi
  EXTRACT_UNIT="$unit"
  docker create --name "$unit" "$image" >/dev/null || { EXTRACT_UNIT=""; return 1; }
  docker cp "$unit:/app-artifacts/deploy/release/migrations.txt" "$out/migrations.txt" >/dev/null || ok=0
  if [ "$ok" = 1 ]; then
    docker cp "$unit:/app-artifacts/server/migrations/." "$out/sql/" >/dev/null || ok=0
  fi
  docker rm -f "$unit" >/dev/null 2>&1 || true
  EXTRACT_UNIT=""
  [ "$ok" = 1 ] && [ -s "$out/migrations.txt" ]
}

# 从目标版本镜像取出部署脚本，校验后原子替换宿主安装副本并重载执行。
# 只在通过安装路径以 root 调用时生效；失败一律降级为继续使用当前脚本。
self_update_from_image() {
  local ver="$1" self_path image unit work tmp ok
  if [ "${WARDEN_DEPLOY_SELF_UPDATED:-0}" = "1" ] || [ "${WARDEN_DEPLOY_SELF_UPDATE:-1}" != "1" ]; then
    return 0
  fi
  if [ "$(id -u)" != "0" ]; then
    return 0
  fi
  case "$0" in
    /*) self_path="$0" ;;
    *) self_path="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")" ;;
  esac
  if [ "$self_path" != "$SELF_TARGET" ]; then
    echo "==> 非安装路径调用，跳过部署脚本自更新（$self_path）"
    return 0
  fi

  image="$CONTROL_IMAGE:$ver"
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker pull "$image" >/dev/null 2>&1 || {
      echo "警告：无法拉取 $image，跳过部署脚本自更新" >&2
      return 0
    }
  fi

  work="$(mktemp -d)"
  unit="warden-self-update-$$-$RANDOM"
  EXTRACT_UNIT="$unit"
  if ! docker create --name "$unit" "$image" >/dev/null 2>&1; then
    EXTRACT_UNIT=""
    rm -rf "$work"
    echo "警告：无法读取 $image，跳过部署脚本自更新" >&2
    return 0
  fi
  ok=1
  docker cp "$unit:/app-artifacts/scripts/deploy-compose.sh" "$work/deploy-compose.sh" >/dev/null 2>&1 || ok=0
  docker rm -f "$unit" >/dev/null 2>&1 || true
  EXTRACT_UNIT=""
  if [ "$ok" != "1" ] || [ ! -s "$work/deploy-compose.sh" ]; then
    rm -rf "$work"
    echo "==> 镜像未携带部署脚本（旧版本镜像），跳过自更新"
    return 0
  fi
  if ! bash -n "$work/deploy-compose.sh" >/dev/null 2>&1; then
    rm -rf "$work"
    echo "警告：镜像内部署脚本语法校验失败，拒绝自更新" >&2
    return 0
  fi
  if cmp -s "$work/deploy-compose.sh" "$self_path"; then
    rm -rf "$work"
    return 0
  fi

  tmp="$(mktemp "$(dirname "$SELF_TARGET")/.warden-deploy.XXXXXX")" || {
    rm -rf "$work"
    echo "警告：无法创建自更新临时文件，继续使用当前脚本" >&2
    return 0
  }
  if ! install -o root -g root -m 755 "$work/deploy-compose.sh" "$tmp" 2>/dev/null; then
    rm -f "$tmp"
    rm -rf "$work"
    echo "警告：写入自更新脚本失败，继续使用当前脚本" >&2
    return 0
  fi
  if ! mkdir -p "$SELF_BACKUP_DIR" 2>/dev/null; then
    rm -f "$tmp"
    rm -rf "$work"
    echo "警告：创建脚本备份目录失败，取消自更新" >&2
    return 0
  fi
  if ! cp -a "$SELF_TARGET" "$SELF_BACKUP_DIR/warden-deploy.$(date +%Y%m%d-%H%M%S)" 2>/dev/null; then
    rm -f "$tmp"
    rm -rf "$work"
    echo "警告：备份当前部署脚本失败，取消自更新" >&2
    return 0
  fi
  mv -f "$tmp" "$SELF_TARGET"
  ls -1t "$SELF_BACKUP_DIR"/warden-deploy.* 2>/dev/null | tail -n +$((KEEP_SCRIPT_BACKUPS + 1)) | xargs -r rm -f
  rm -rf "$work"
  echo "==> 部署脚本已自更新，重载执行: $SELF_TARGET"
  exec env WARDEN_DEPLOY_SELF_UPDATED=1 "$SELF_TARGET" "$ver"
}

backup_database() {
  local name partial errfile
  mkdir -p "$BACKUP_DIR"
  name="$BACKUP_DIR/db-${PREV_VERSION:-unknown}-$(date +%Y%m%d-%H%M%S).dump"
  partial="$name.partial"
  errfile="$WORK_DIR/pg_dump.err"
  echo "==> 备份数据库（pg_dump -Fc）"
  if ! compose_exec exec -T "$PG_SERVICE" pg_dump -U "$PG_USER" -d "$PG_DB" -Fc >"$partial" 2>"$errfile"; then
    cat "$errfile" >&2 || true
    rm -f "$partial"
    echo "数据库备份失败，终止部署（确认无需备份后可设 SKIP_DB_BACKUP=1）" >&2
    return 1
  fi
  if [ ! -s "$partial" ]; then
    rm -f "$partial"
    echo "数据库备份为空，终止部署" >&2
    return 1
  fi
  chmod 600 "$partial"
  mv "$partial" "$name"
  echo "数据库已备份: $name"
  ls -1t "$BACKUP_DIR"/db-*.dump 2>/dev/null | tail -n +$((KEEP_DB_BACKUPS + 1)) | xargs -r rm -f
}

apply_migrations() {
  local new_dir prev_dir pending f count initialized
  if [ "$MIGRATION_MODE" = "skip" ]; then
    echo "==> 跳过数据库迁移（MIGRATION_MODE=skip）"
    return 0
  fi
  if ! postgres_running; then
    echo "PostgreSQL 服务（$PG_SERVICE）未运行：先启动数据库，或设置 MIGRATION_MODE=skip（外部托管库）" >&2
    return 1
  fi
  initialized="$(psql_exec -tAc "SELECT to_regclass('public.principals') IS NOT NULL")"
  if [ "$initialized" != "t" ]; then
    echo "数据库尚未初始化（缺少基础表）：首次部署请按 docs/guides.md 先执行 make migrate" >&2
    return 1
  fi

  WORK_DIR="$(mktemp -d)"
  new_dir="$WORK_DIR/new"
  prev_dir="$WORK_DIR/prev"
  pending="$WORK_DIR/pending.txt"

  if ! extract_release_artifacts "$VERSION" "$new_dir"; then
    echo "无法从镜像 $CONTROL_IMAGE:$VERSION 读取发布迁移清单" >&2
    return 1
  fi

  : >"$pending"
  case "$MIGRATION_MODE" in
    all)
      echo "==> 迁移模式 all：重放发布清单中的全部迁移"
      cp "$new_dir/migrations.txt" "$pending"
      ;;
    auto)
      if [ "$PREV_VERSION" = "$VERSION" ]; then
        echo "==> 迁移模式 auto：版本未变化，跳过迁移"
        : >"$pending"
      elif [ -n "$PREV_VERSION" ] && extract_release_artifacts "$PREV_VERSION" "$prev_dir"; then
        echo "==> 迁移模式 auto：对比上一版本 $PREV_VERSION 的发布清单"
        grep -Fxvf "$prev_dir/migrations.txt" "$new_dir/migrations.txt" >"$pending" || true
      else
        echo "==> 迁移模式 auto：读不到上一版本（${PREV_VERSION:-未知}）的清单，回退为全量重放" >&2
        cp "$new_dir/migrations.txt" "$pending"
      fi
      ;;
  esac

  count="$(grep -cve '^[[:space:]]*$' "$pending" || true)"
  if [ "$count" -eq 0 ]; then
    echo "==> 无新增迁移，跳过"
    return 0
  fi
  echo "==> 待应用迁移 $count 个"
  if [ "$SKIP_DB_BACKUP" = "1" ]; then
    echo "警告：SKIP_DB_BACKUP=1，已跳过迁移前数据库备份" >&2
  else
    backup_database || return 1
  fi

  while IFS= read -r f; do
    f="${f%$'\r'}"
    [ -z "$f" ] && continue
    if [[ ! "$f" =~ ^[0-9]{3}_[A-Za-z0-9_.-]+\.sql$ ]]; then
      echo "发布清单包含非法文件名: $f" >&2
      return 1
    fi
    if [ ! -f "$new_dir/sql/$f" ]; then
      echo "镜像内缺少迁移文件: $f" >&2
      return 1
    fi
    echo "    applying $f"
    if ! psql_exec <"$new_dir/sql/$f" >/dev/null; then
      echo "迁移失败并终止（控制面仍为 ${PREV_VERSION:-当前版本}）: $f" >&2
      return 1
    fi
  done <"$pending"

  MIGRATIONS_APPLIED="$count"
  echo "==> 迁移完成：$count 个文件"
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

  # 显式给定权限：镜像/临时目录可能是 700，直接 -a 会把 dist 变成不可读导致 nginx 403。
  if ! rsync -rlt --delete --chmod=D755,F644 "$tmp"/ "$WEB_ROOT"/; then
    rm -rf "$tmp"
    echo "同步前端失败，回滚静态文件" >&2
    rm -rf "$WEB_ROOT"
    tar xzf "$backup" -C "$(dirname "$WEB_ROOT")"
    chmod 755 "$WEB_ROOT"
    return 1
  fi
  chmod 755 "$WEB_ROOT"
  rm -rf "$tmp"
  echo "前端已更新: $WEB_ROOT（$ver）"

  ls -1t "$BACKUP_DIR"/dist-*.tar.gz 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f
}

echo "上一个版本: ${PREV_VERSION:-无}"

if ! docker compose -f "$COMPOSE_FILE" pull warden-control; then
  echo "控制面镜像拉取失败" >&2
  exit 1
fi

# 镜像内自带部署脚本时，先自更新再继续；会以新脚本重载执行。
self_update_from_image "$VERSION"

if [ -n "$WEB_ROOT" ] && ! docker pull "$REGISTRY/warden-web:$VERSION"; then
  echo "前端镜像拉取失败" >&2
  exit 1
fi

# 先迁移、后切版本：迁移失败时旧版本容器继续服务；新 schema 需对旧版本兼容。
if ! apply_migrations; then
  echo "数据库迁移阶段失败，终止部署（未切换镜像）" >&2
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
