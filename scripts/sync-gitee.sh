#!/usr/bin/env bash
# 把 GitHub master 的合并结果快进同步到 Gitee 主仓库 master。
#
# 现在 GitHub 承担合并门禁（PR + CI 必过），Gitee 作为国内镜像。
# 与 sync-github.sh（Gitee 快照 -> GitHub）方向相反。
#
# 用法：
#   scripts/sync-gitee.sh     # 快进同步 github/master -> origin/master
#   make sync-gitee
#
# 设计：
# - 只做 fast-forward：Gitee master 必须是 GitHub master 的祖先，否则说明
#   Gitee 侧仍有独立提交，脚本中止并提示人工处理，绝不 force push。
# - 网络：GitHub 走系统代理（默认 127.0.0.1:7897，可用 GITHUB_PROXY 覆盖），
#   Gitee 直连（须清掉本机死代理变量）。
# 依赖：git。

set -euo pipefail

GITHUB_REPO="${GITHUB_REPO:-warden-platform/warden}"
GITHUB_BRANCH="${GITHUB_BRANCH:-master}"
GITEE_REMOTE="${GITEE_REMOTE:-origin}"
GITEE_BRANCH="${GITEE_BRANCH:-master}"
GITHUB_PROXY="${GITHUB_PROXY:-http://127.0.0.1:7897}"

cd "$(git rev-parse --show-toplevel)"

# GitHub 拉取走代理
git -c http.proxy="$GITHUB_PROXY" fetch --quiet "https://github.com/$GITHUB_REPO.git" "$GITHUB_BRANCH"
GH_COMMIT="$(git rev-parse FETCH_HEAD)"

# Gitee 直连，清掉可能失效的本机代理
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
git fetch --quiet "$GITEE_REMOTE" "$GITEE_BRANCH"
GITEE_COMMIT="$(git rev-parse "$GITEE_REMOTE/$GITEE_BRANCH")"

if [ "$GH_COMMIT" = "$GITEE_COMMIT" ]; then
  echo "Gitee 已是最新（$GITEE_BRANCH @ $(git rev-parse --short "$GH_COMMIT")），无需同步。"
  exit 0
fi

if ! git merge-base --is-ancestor "$GITEE_COMMIT" "$GH_COMMIT"; then
  echo "Gitee $GITEE_BRANCH 存在 GitHub 上没有的提交（历史已分叉），拒绝 force push。" >&2
  echo "  gitee : $GITEE_COMMIT" >&2
  echo "  github: $GH_COMMIT" >&2
  echo "请人工确认后再同步。" >&2
  exit 1
fi

if ! push_out="$(git push "$GITEE_REMOTE" "$GH_COMMIT:refs/heads/$GITEE_BRANCH" 2>&1)"; then
  echo "$push_out" >&2
  echo "推送到 Gitee 失败，请检查网络或凭据。" >&2
  exit 1
fi
echo "$push_out"

# Gitee 保护分支会拦截直推并自动创建合并 PR，此时 master 并不会真的前进，
# 必须复核远程分支是否已指向目标提交，避免误报成功。
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
git fetch --quiet "$GITEE_REMOTE" "$GITEE_BRANCH"
NEW_GITEE="$(git rev-parse "$GITEE_REMOTE/$GITEE_BRANCH")"
if [ "$NEW_GITEE" != "$GH_COMMIT" ]; then
  pr_url="$(printf '%s\n' "$push_out" | grep -oE 'https://gitee\.com/[^ ]+/pulls/[0-9]+' | head -1)"
  echo "Gitee $GITEE_BRANCH 是受保护分支，直推被拦截，当前仍停在 $(git rev-parse --short "$NEW_GITEE")。" >&2
  if [ -n "$pr_url" ]; then
    echo "Gitee 已自动创建同步 PR，请合并后再次运行本脚本：$pr_url" >&2
  else
    echo "请到 Gitee 合并因保护分支生成的 PR，或调整分支保护允许同步账号直推。" >&2
  fi
  exit 2
fi

echo "已同步 Gitee $GITEE_BRANCH -> $(git rev-parse --short "$GH_COMMIT")（来自 GitHub）"
