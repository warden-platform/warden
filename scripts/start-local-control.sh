#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
LOG_DIR=/tmp
# warden-control defaults to 8080. When unauth (the local OIDC IdP) also owns
# 8080, set WARDEN_PORT to keep the two services apart (e.g. 8090).
CONTROL_PORT="${WARDEN_PORT:-8080}"

if [[ ! -x "$ROOT_DIR/server/bin/warden-control" ]]; then
  echo "warden-control binary is missing; build it first with: make server-build" >&2
  exit 1
fi
if [[ ! -f "$ROOT_DIR/.secrets/admin_password" ]]; then
  echo "missing $ROOT_DIR/.secrets/admin_password" >&2
  exit 1
fi
if [[ ! -f "$ROOT_DIR/.secrets/jwt_signing_key" ]]; then
  echo "missing $ROOT_DIR/.secrets/jwt_signing_key (create a unique 32+ byte secret; do not reuse task_signing_key)" >&2
  exit 1
fi

existing_pid=$(lsof -tiTCP:"$CONTROL_PORT" -sTCP:LISTEN 2>/dev/null || true)
if [[ -n "$existing_pid" ]]; then
  echo "stopping existing warden-control: $existing_pid"
  kill -TERM $existing_pid 2>/dev/null || true
  for _ in {1..20}; do
    if ! lsof -tiTCP:"$CONTROL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
fi

CONTROL_ENV=(
  WARDEN_PORT="$CONTROL_PORT"
  WARDEN_DATABASE_URL_FILE="$ROOT_DIR/.secrets/database_url"
  WARDEN_TASK_SIGNING_KEY_FILE="$ROOT_DIR/.secrets/task_signing_key"
  WARDEN_JWT_SIGNING_KEY_FILE="$ROOT_DIR/.secrets/jwt_signing_key"
  WARDEN_CREDENTIAL_ENCRYPTION_KEY_FILE="$ROOT_DIR/.secrets/credential_encryption_key"
  WARDEN_ADMIN_API_KEY_FILE="$ROOT_DIR/.secrets/agent_api_key"
  WARDEN_ADMIN_PASSWORD_FILE="$ROOT_DIR/.secrets/admin_password"
  WARDEN_COOKIE_SECURE=false
  WARDEN_CORS_ORIGIN="${WARDEN_CORS_ORIGIN:-http://localhost:4173}"
  WARDEN_PUBLIC_CONTROL_URL="${WARDEN_PUBLIC_CONTROL_URL:-http://localhost:8080}"
  WARDEN_ENABLE_LEGACY_CONNECTOR_KEY="${WARDEN_ENABLE_LEGACY_CONNECTOR_KEY:-false}"
  WARDEN_ARTIFACT_ROOT="$ROOT_DIR"
)

# Optional OIDC login. WARDEN_OIDC_ENABLED=true is the explicit master switch;
# =false forces SSO off. With the switch unset, a configured WARDEN_OIDC_ISSUER
# still enables OIDC (legacy contract). See scripts/oidc-local.env.example.
oidc_switch="$(printf '%s' "${WARDEN_OIDC_ENABLED:-}" | tr '[:upper:]' '[:lower:]')"
case "$oidc_switch" in
  false|0|no|off)
    echo "OIDC disabled by WARDEN_OIDC_ENABLED=$oidc_switch"
    ;;
  ""|true|1|yes|on)
    if [[ -n "$oidc_switch" || -n "${WARDEN_OIDC_ISSUER:-}" ]]; then
      : "${WARDEN_OIDC_ISSUER:?WARDEN_OIDC_ISSUER is required when OIDC is enabled}"
      : "${WARDEN_OIDC_CLIENT_ID:?WARDEN_OIDC_CLIENT_ID is required when OIDC is enabled}"
      oidc_secret_file="${WARDEN_OIDC_CLIENT_SECRET_FILE:-$ROOT_DIR/.secrets/oidc_client_secret}"
      if [[ ! -f "$oidc_secret_file" ]]; then
        echo "missing OIDC client secret file: $oidc_secret_file" >&2
        echo "write the client secret as a single line, with no quotes or trailing blank lines" >&2
        exit 1
      fi
      # The redirect URI must match the IdP's exact whitelist byte-for-byte. The
      # default targets the Vite dev origin on :8081 (which proxies /api to
      # warden-control on WARDEN_PORT); landing the callback directly on warden-control
      # would 302 to / with no SPA to serve it.
      oidc_redirect_url="${WARDEN_OIDC_REDIRECT_URL:-http://127.0.0.1:8081/api/v1/auth/oidc/callback}"
      CONTROL_ENV+=(
        WARDEN_OIDC_ISSUER="$WARDEN_OIDC_ISSUER"
        WARDEN_OIDC_CLIENT_ID="$WARDEN_OIDC_CLIENT_ID"
        WARDEN_OIDC_CLIENT_SECRET_FILE="$oidc_secret_file"
        WARDEN_OIDC_REDIRECT_URL="$oidc_redirect_url"
        WARDEN_OIDC_DEFAULT_TENANT_ID="${WARDEN_OIDC_DEFAULT_TENANT_ID:-platform}"
        WARDEN_OIDC_DEFAULT_ROLE="${WARDEN_OIDC_DEFAULT_ROLE:-user}"
      )
      if [[ -n "$oidc_switch" ]]; then
        CONTROL_ENV+=(WARDEN_OIDC_ENABLED="$oidc_switch")
      fi
      echo "OIDC enabled: issuer=$WARDEN_OIDC_ISSUER redirect=$oidc_redirect_url default_role=${WARDEN_OIDC_DEFAULT_ROLE:-user}"
    else
      echo "OIDC disabled (set WARDEN_OIDC_ENABLED=true plus issuer/client/secret to enable; see scripts/oidc-local.env.example)"
    fi
    ;;
  *)
    echo "WARDEN_OIDC_ENABLED must be true or false (got: $WARDEN_OIDC_ENABLED)" >&2
    exit 1
    ;;
esac

if [[ -f "$ROOT_DIR/.secrets/dingtalk_webhook" && -f "$ROOT_DIR/.secrets/dingtalk_secret" ]]; then
  CONTROL_ENV+=(WARDEN_DINGTALK_WEBHOOK_FILE="$ROOT_DIR/.secrets/dingtalk_webhook")
  CONTROL_ENV+=(WARDEN_DINGTALK_SECRET_FILE="$ROOT_DIR/.secrets/dingtalk_secret")
elif [[ -f "$ROOT_DIR/.secrets/dingtalk_webhook" || -f "$ROOT_DIR/.secrets/dingtalk_secret" ]]; then
  echo "dingtalk_webhook and dingtalk_secret must both exist under $ROOT_DIR/.secrets" >&2
  exit 1
fi

DINGTALK_COOLAPP_FILES=(dingtalk_app_key dingtalk_app_secret)
coolapp_count=0
for file in "${DINGTALK_COOLAPP_FILES[@]}"; do
  [[ -f "$ROOT_DIR/.secrets/$file" ]] && coolapp_count=$((coolapp_count + 1))
done
if [[ "$coolapp_count" -eq "${#DINGTALK_COOLAPP_FILES[@]}" ]]; then
  CONTROL_ENV+=(WARDEN_DINGTALK_APP_KEY_FILE="$ROOT_DIR/.secrets/dingtalk_app_key")
  CONTROL_ENV+=(WARDEN_DINGTALK_APP_SECRET_FILE="$ROOT_DIR/.secrets/dingtalk_app_secret")
elif [[ "$coolapp_count" -ne 0 ]]; then
  echo "dingtalk_app_key and dingtalk_app_secret must both exist under $ROOT_DIR/.secrets" >&2
  exit 1
fi

DINGTALK_CARD_FILES=(dingtalk_card_template_id dingtalk_open_conversation_id dingtalk_robot_code)
card_count=0
for file in "${DINGTALK_CARD_FILES[@]}"; do
  [[ -f "$ROOT_DIR/.secrets/$file" ]] && card_count=$((card_count + 1))
done
if [[ "$card_count" -eq "${#DINGTALK_CARD_FILES[@]}" ]]; then
  if [[ "$coolapp_count" -ne "${#DINGTALK_COOLAPP_FILES[@]}" ]]; then
    echo "interactive cards also require dingtalk_app_key and dingtalk_app_secret" >&2
    exit 1
  fi
  CONTROL_ENV+=(WARDEN_DINGTALK_CARD_TEMPLATE_ID_FILE="$ROOT_DIR/.secrets/dingtalk_card_template_id")
  CONTROL_ENV+=(WARDEN_DINGTALK_OPEN_CONVERSATION_ID_FILE="$ROOT_DIR/.secrets/dingtalk_open_conversation_id")
  CONTROL_ENV+=(WARDEN_DINGTALK_ROBOT_CODE_FILE="$ROOT_DIR/.secrets/dingtalk_robot_code")
elif [[ "$card_count" -ne 0 ]]; then
  echo "dingtalk_card_template_id, dingtalk_open_conversation_id and dingtalk_robot_code must all exist under $ROOT_DIR/.secrets" >&2
  exit 1
fi

# Foreground mode is useful when the caller already owns process supervision
# (e.g. a dev task runner), where a detached nohup child would be reaped as
# soon as the launching shell exits.
if [[ "${WARDEN_FOREGROUND:-}" == "1" ]]; then
  exec env "${CONTROL_ENV[@]}" "$ROOT_DIR/server/bin/warden-control"
fi

nohup env "${CONTROL_ENV[@]}" \
  "$ROOT_DIR/server/bin/warden-control" \
  >"$LOG_DIR/warden-control-direct.log" 2>"$LOG_DIR/warden-control-direct.err" </dev/null &

for _ in {1..20}; do
  if lsof -tiTCP:"$CONTROL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "warden-control is listening on :$CONTROL_PORT"
    exit 0
  fi
  sleep 0.25
done

echo "warden-control failed to start; inspect $LOG_DIR/warden-control-direct.err" >&2
tail -n 40 "$LOG_DIR/warden-control-direct.err" >&2 || true
exit 1
