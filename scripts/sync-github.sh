#!/usr/bin/env bash
# 把 gitee 主仓库的当前树快照同步到 GitHub 镜像（warden-platform/warden，main 分支）。
#
# 用法：
#   scripts/sync-github.sh            # 同步 HEAD 树（追加一个快照 commit）
#   scripts/sync-github.sh v0.4.1     # 发版同步：v0.4.1 的树 + 推 tag + 建 GitHub Release
#                                     #   Release 正文自动提取 CHANGELOG.md 对应版本小节
#   make sync-github TAG=v0.4.1       # 同上
#
# 设计：
# - GitHub 镜像是快照历史（每个同步点一个 commit），不携带 gitee 完整历史。
# - 用 git plumbing（read-tree/write-tree/commit-tree）构造提交，不删除任何文件。
# - 树与镜像 HEAD 完全一致时跳过 commit（幂等）；tag/Release 已存在时跳过。
# - 网络：gitee 侧直连（本机代理变量是死代理，须 unset）；GitHub 侧走系统代理
#   （默认 127.0.0.1:7897，可用 GITHUB_PROXY 覆盖）。gh 需已授权（gh auth login）。
#
# 依赖：git、gh、tar、awk。在仓库根目录运行。

set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-warden-platform/warden}"
GITHUB_BRANCH="${GITHUB_BRANCH:-main}"
GITHUB_PROXY="${GITHUB_PROXY:-http://127.0.0.1:7897}"
REF="${1:-HEAD}"

cd "$(git rev-parse --show-toplevel)"

# gitee 侧操作必须去掉死代理变量
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

if ! git rev-parse --verify --quiet "$REF^{commit}" >/dev/null; then
  echo "本地找不到 ref：$REF，尝试从 gitee 拉取 tag…" >&2
  git fetch origin "refs/tags/$REF:refs/tags/$REF"
fi
COMMIT="$(git rev-parse --verify "$REF^{commit}")"
SHORT="$(git rev-parse --short "$COMMIT")"

# 导出目标树（仅 tracked 文件，gitignore 的 .env/secrets 不会进入快照）
EXPORT_DIR="$(mktemp -d)"
git archive "$COMMIT" | tar -x -C "$EXPORT_DIR"

# 浅克隆镜像（走代理）
MIRROR="$(mktemp -d)"
git -c http.proxy="$GITHUB_PROXY" clone --quiet --branch "$GITHUB_BRANCH" --single-branch \
  "https://github.com/$GITHUB_REPO.git" "$MIRROR"

# 用临时 index 从导出目录构造树，parent 指向镜像 HEAD
export GIT_INDEX_FILE="$MIRROR/.git/sync-index"
git -C "$MIRROR" read-tree --empty
git -C "$MIRROR" --work-tree="$EXPORT_DIR" add -A
TREE="$(git -C "$MIRROR" write-tree)"
PARENT="$(git -C "$MIRROR" rev-parse "origin/$GITHUB_BRANCH")"
PARENT_TREE="$(git -C "$MIRROR" rev-parse "$PARENT^{tree}")"

if [ "$TREE" = "$PARENT_TREE" ]; then
  echo "树与镜像 HEAD 一致（gitee@${SHORT}），无需新 commit。"
  NEW_COMMIT="$PARENT"
else
  if [ "$REF" = "HEAD" ]; then
    MSG="chore: 同步 gitee master@$SHORT"
  else
    MSG="chore: Warden $REF"
  fi
  NEW_COMMIT="$(git -C "$MIRROR" commit-tree "$TREE" -p "$PARENT" -m "$MSG")"
  git -C "$MIRROR" update-ref "refs/heads/$GITHUB_BRANCH" "$NEW_COMMIT"
  git -C "$MIRROR" -c http.proxy="$GITHUB_PROXY" push origin "$GITHUB_BRANCH"
  echo "已推送快照 commit：$(git -C "$MIRROR" rev-parse --short "$NEW_COMMIT")（gitee@${SHORT}）"
fi
unset GIT_INDEX_FILE

# 发版模式：推 tag + 建 Release（正文取 CHANGELOG 对应小节）
if [ "$REF" != "HEAD" ]; then
  if git -C "$MIRROR" rev-parse --verify --quiet "refs/tags/$REF" >/dev/null; then
    echo "tag $REF 已在镜像，跳过。"
  else
    git -C "$MIRROR" tag -a "$REF" "$NEW_COMMIT" -m "Warden $REF"
    git -C "$MIRROR" -c http.proxy="$GITHUB_PROXY" push origin "refs/tags/$REF"
    echo "已推送 tag：$REF"
  fi

  if HTTPS_PROXY="$GITHUB_PROXY" HTTP_PROXY="$GITHUB_PROXY" \
    gh release view "$REF" --repo "$GITHUB_REPO" >/dev/null 2>&1; then
    echo "Release $REF 已存在，跳过。"
  else
    VERSION="${REF#v}"
    NOTES="$(git show "$COMMIT:CHANGELOG.md" | awk -v ver="$VERSION" '
      $0 ~ "^## \\[" ver "\\]" { f = 1; next }
      /^## \[/ { f = 0 }
      f { print }
    ')"
    if [ -z "$NOTES" ]; then
      NOTES="同步自 gitee@${SHORT}，变更见 CHANGELOG.md。"
    fi
    NOTES_FILE="$(mktemp)"
    printf '%s\n' "$NOTES" > "$NOTES_FILE"
    HTTPS_PROXY="$GITHUB_PROXY" HTTP_PROXY="$GITHUB_PROXY" \
      gh release create "$REF" --repo "$GITHUB_REPO" --title "Warden $REF" --notes-file "$NOTES_FILE"
    echo "已创建 GitHub Release：$REF"
  fi
fi

echo "完成：https://github.com/$GITHUB_REPO"