#!/usr/bin/env bash
# 把 Warden 控制面/管理端镜像发布到阿里云 ACR（在构建机上跑，如 ECS）。
#
# 用法：
#   scripts/publish-acr.sh [version]        # 默认 v0.4.0
#   ACR_REGISTRY / ACR_NAMESPACE 可覆盖（默认 registry.cn-hangzhou.aliyuncs.com / warden-stable）
#
# 凭据：.secrets/acr_username 与 .secrets/acr_password（600，已 gitignore）。
# 流程：login → 构建 web/control（国内镜像源）→ 推 version 与 latest 双 tag →
#       manifest 验证。只打 warden-*:<version> 本地 tag，不改生产在用的 latest。
# 日志：追加到仓库根 acr-publish.log；建议用 systemd-run 托管以免 SSH 断开中断
# （systemd 临时服务不继承工作目录，脚本必须用绝对路径）：
#   systemd-run --unit=warden-acr-publish --collect bash /root/warden/scripts/publish-acr.sh v0.4.0
# 失败排查：tail acr-publish.log；systemctl is-failed warden-acr-publish。
# ACR 仓库不存在且命名空间未开自动创建时 push 会拒绝——先在 ACR 控制台建仓库。
# 注意：必须 --provenance=false --sbom=false——ACR 个人版不支持 BuildKit 默认的
# attestation manifest（OCI empty descriptor），push 层成功但 manifest 被拒。

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
VERSION="${1:-v0.4.0}"
REGISTRY="${ACR_REGISTRY:-registry.cn-hangzhou.aliyuncs.com}"
NAMESPACE="${ACR_NAMESPACE:-warden-stable}"
SECRETS_DIR="$ROOT_DIR/.secrets"

exec >>"$ROOT_DIR/acr-publish.log" 2>&1
echo "=== publish-acr $VERSION $(date -Is) ==="
trap 'echo "=== PUBLISH-FAILED $VERSION $(date -Is) ==="' ERR

for f in acr_username acr_password; do
  if [[ ! -r "$SECRETS_DIR/$f" ]]; then
    echo "missing $SECRETS_DIR/$f (600)" >&2
    exit 1
  fi
done

cd "$ROOT_DIR"
docker login --username "$(cat "$SECRETS_DIR/acr_username")" \
  --password-stdin "$REGISTRY" < "$SECRETS_DIR/acr_password"

echo "--- build warden-web:$VERSION"
docker build -f web/Dockerfile \
  --provenance=false --sbom=false \
  --build-arg NODE_IMAGE=docker.m.daocloud.io/library/node:22-alpine \
  --build-arg NGINX_IMAGE=docker.m.daocloud.io/nginxinc/nginx-unprivileged:1.27-alpine \
  --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
  -t "warden-web:$VERSION" web/

echo "--- build warden-control:$VERSION"
docker build -f server/Dockerfile --build-arg TARGET=warden-control \
  --provenance=false --sbom=false \
  --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.26-alpine \
  --build-arg RUNTIME_IMAGE=gcr.m.daocloud.io/distroless/static-debian12:nonroot \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t "warden-control:$VERSION" .

publish() {
  local local_name="$1" repo="$2"
  docker tag "$local_name:$VERSION" "$REGISTRY/$NAMESPACE/$repo:$VERSION"
  docker tag "$local_name:$VERSION" "$REGISTRY/$NAMESPACE/$repo:latest"
  docker push "$REGISTRY/$NAMESPACE/$repo:$VERSION"
  docker push "$REGISTRY/$NAMESPACE/$repo:latest"
  docker manifest inspect "$REGISTRY/$NAMESPACE/$repo:$VERSION" >/dev/null
  echo "--- published $REGISTRY/$NAMESPACE/$repo:$VERSION (+latest)"
}

publish warden-web warden-web
publish warden-control warden

echo "=== PUBLISH-OK $VERSION $(date -Is) ==="