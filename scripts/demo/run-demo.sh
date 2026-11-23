#!/usr/bin/env bash
# 一键起本地演示环境（免构建，用 ACR 公开镜像）。用法：make demo
#
# 流程：生成 secret（如缺）→ 起 postgres → 建角色 + 迁移 → 起控制面/管理端
#       → 灌演示数据 → 打印访问地址与演示账号。
# 仅 localhost http，演示账号弱密码 12345678（仅限本机演示，勿对生产运行）。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

COMPOSE_DEMO="docker compose -f deploy/docker-compose.demo.yaml"
DEMO_IMAGE_TAG="v0.4.2"

# 1) 生成 secret（已存在则跳过；init-prod-secrets 遇存在即拒绝，可安全重复调用）
if [ ! -f .secrets/warden_database_url ]; then
	echo "==> 生成 .secrets/（首次）"
	bash scripts/init-prod-secrets.sh
else
	echo "==> 复用已有 .secrets/"
fi

# 2) 先起 postgres，等它健康（建角色/迁移依赖它）
echo "==> 启动 postgres"
$COMPOSE_DEMO up -d postgresql
for _ in $(seq 1 20); do
	if $COMPOSE_DEMO exec -T postgresql pg_isready -U warden -d warden >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

# 3) 建应用角色（warden_app/warden_privileged，RLS 地基）+ 迁移
echo "==> 建角色 + 迁移"
COMPOSE="$COMPOSE_DEMO" make init-db-roles
COMPOSE="$COMPOSE_DEMO" make migrate

# 4) 起控制面 + 管理端
echo "==> 启动控制面与管理端"
$COMPOSE_DEMO up -d

# 5) 灌演示数据（--dry-run 生成 SQL，经 demo postgres 应用，避免宿主机 psql/DSN 依赖）
echo "==> 灌演示数据"
PG_PW=$(cat .secrets/postgres_password)
DATABASE_URL="postgres://warden:${PG_PW}@127.0.0.1:5433/warden" \
	python3 scripts/demo/seed_demo_data.py --dry-run >/dev/null
$COMPOSE_DEMO exec -T postgresql psql -U warden -d warden -v ON_ERROR_STOP=1 -1 \
	< /tmp/warden-demo-seed.sql >/dev/null

cat <<EOF

演示环境已就绪（控制面镜像 warden:${DEMO_IMAGE_TAG}）：
  管理端：  http://localhost:8081
  控制面：  http://localhost:8080/api/v1/health

  演示账号：demo@acme / 12345678（另有 zhangsan、lisi 同密码；租户 acme/chunf）
  平台超管：demo 站超管账号在 .secrets/warden_admin_password（不回显，自行查看）

  停止并清理：docker compose -f deploy/docker-compose.demo.yaml down -v
EOF