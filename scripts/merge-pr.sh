#!/usr/bin/env bash
# 自动合并 Gitee PR：触发 ci-gate → 校验通过 → 审查/测试通过 → 合并（删源分支）。
# 绕开 Gitee 社区版不稳定的保护分支「检查项」状态匹配，由本脚本程序化把关：
# ci-gate 不 SUCCESS 绝不合并；合并前复核头提交未变化，并满足审查/测试审核项。
# 配合保护分支「禁止直推 + 评审模式」使用。
#
# 用法：scripts/merge-pr.sh <PR编号> [仓库 owner/repo]
#   例：scripts/merge-pr.sh 2
# 依赖：gitee CLI 已登录（gitee auth login），仓库含 .workflow/ci.yml。
# 可覆盖：CI_FILE（默认 ci.yml）、TIMEOUT（默认 900 秒）、POLL（默认 15 秒）。
set -euo pipefail

PR="${1:?用法: scripts/merge-pr.sh <PR编号> [owner/repo]}"
REPO="${2:-warden-platform/warden}"
CI_FILE="${CI_FILE:-ci.yml}"
TIMEOUT="${TIMEOUT:-900}"
POLL="${POLL:-15}"

command -v gitee >/dev/null 2>&1 || { echo "需要 gitee CLI（gitee auth login）" >&2; exit 1; }

echo "==> 读取 PR #$PR 信息"
PR_JSON=$(gitee pr view "$PR" -R "$REPO" --json --no-tui)
STATE=$(printf '%s' "$PR_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin).get('state',''))")
HEAD_REF=$(printf '%s' "$PR_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin).get('head',{}).get('ref',''))")
HEAD_SHA=$(printf '%s' "$PR_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin).get('head',{}).get('sha',''))")
if [[ "$STATE" != "open" ]]; then
  echo "PR #$PR 状态为 $STATE（非 open），中止。" >&2; exit 1
fi
echo "    源分支=$HEAD_REF 头提交=$HEAD_SHA"

echo "==> 在 PR 头分支触发 $CI_FILE"
BUILD_ID=$(gitee pipeline run -R "$REPO" --ref "$HEAD_REF" --file "$CI_FILE" --json --no-tui \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['id'])")
echo "    ci-gate 构建 #$BUILD_ID，等待通过（超时 ${TIMEOUT}s）"

elapsed=0
while true; do
  STATUS=$(gitee pipeline build status "$BUILD_ID" -R "$REPO" --json --no-tui \
    | python3 -c "import json,sys;print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "UNKNOWN")
  case "$STATUS" in
    SUCCESS) echo "    ci-gate SUCCESS"; break ;;
    FAILED|CANCELLED|CANCELED)
      echo "    ci-gate $STATUS——拒绝合并。查看：gitee pipeline build view $BUILD_ID -R $REPO" >&2
      exit 1 ;;
  esac
  if (( elapsed >= TIMEOUT )); then
    echo "    等待超时（${TIMEOUT}s），ci-gate 仍 $STATUS——拒绝合并。" >&2; exit 1
  fi
  sleep "$POLL"; elapsed=$((elapsed + POLL))
done

echo "==> 复核 PR 头提交未被更新"
NEW_SHA=$(gitee pr view "$PR" -R "$REPO" --json --no-tui \
  | python3 -c "import json,sys;print(json.load(sys.stdin).get('head',{}).get('sha',''))")
if [[ "$NEW_SHA" != "$HEAD_SHA" ]]; then
  echo "    PR 头提交已变化（${HEAD_SHA:0:8} → ${NEW_SHA:0:8}），本次 ci-gate 结果作废，请重跑。" >&2
  exit 1
fi

echo "==> 批准 PR #$PR"
gitee pr review "$PR" -R "$REPO" -b "ci-gate 通过（构建 #$BUILD_ID），自动批准。" --no-tui >/dev/null

echo "==> 标记 PR #$PR 测试通过（ci-gate #$BUILD_ID 已通过）"
gitee api -X POST "/repos/$REPO/pulls/$PR/test" -f force=true >/dev/null

echo "==> 合并 PR #$PR（删源分支）"
gitee pr merge "$PR" -R "$REPO" --delete-branch --no-tui
echo "==> 完成：PR #$PR 已合并。"