#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BINARY="$ROOT_DIR/server/bin/warden-control"

if [[ ! -x "$BINARY" ]]; then
  echo "warden-control binary is missing; build it first with: make server-build" >&2
  exit 1
fi

cd "$ROOT_DIR"

# Warden's outbound targets (local IdP, DingTalk, Bailian) are local or CN
# endpoints; a shell-level proxy (e.g. Clash on 127.0.0.1:7897) that is not
# running would break every model/IdP call. Drop inherited proxy variables.
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

if [[ "${WARDEN_FOREGROUND:-}" == "1" ]]; then
  exec "$BINARY"
fi

nohup "$BINARY" \
  > /tmp/warden-control-direct.log \
  2> /tmp/warden-control-direct.err \
  </dev/null &

echo "warden-control started; log: /tmp/warden-control-direct.log"
