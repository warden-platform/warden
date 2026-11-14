#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
LOG_DIR=/tmp
# pam-control defaults to 8080. When unauth (the local OIDC IdP) also owns
# 8080, set PAM_PORT to keep the two services apart (e.g. 8090).
CONTROL_PORT="${PAM_PORT:-8080}"

if [[ ! -x "$ROOT_DIR/server/bin/pam-control" ]]; then
  echo "pam-control binary is missing; build it first with: make server-build" >&2
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
  echo "stopping existing pam-control: $existing_pid"
  kill -TERM $existing_pid 2>/dev/null || true
  for _ in {1..20}; do
    if ! lsof -tiTCP:"$CONTROL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
fi

CONTROL_ENV=(
  PAM_PORT="$CONTROL_PORT"
  PAM_DATABASE_URL_FILE="$ROOT_DIR/.secrets/database_url"
  PAM_TASK_SIGNING_KEY_FILE="$ROOT_DIR/.secrets/task_signing_key"
  PAM_JWT_SIGNING_KEY_FILE="$ROOT_DIR/.secrets/jwt_signing_key"
  PAM_CREDENTIAL_ENCRYPTION_KEY_FILE="$ROOT_DIR/.secrets/credential_encryption_key"
  PAM_ADMIN_API_KEY_FILE="$ROOT_DIR/.secrets/agent_api_key"
  PAM_ADMIN_PASSWORD_FILE="$ROOT_DIR/.secrets/admin_password"
  PAM_COOKIE_SECURE=false
  PAM_CORS_ORIGIN="${PAM_CORS_ORIGIN:-http://localhost:4173}"
  PAM_PUBLIC_CONTROL_URL="${PAM_PUBLIC_CONTROL_URL:-http://localhost:8080}"
  PAM_ENABLE_LEGACY_CONNECTOR_KEY="${PAM_ENABLE_LEGACY_CONNECTOR_KEY:-false}"
  PAM_ARTIFACT_ROOT="$ROOT_DIR"
)

# Optional OIDC login. Export PAM_OIDC_* before invoking this script (see
# scripts/oidc-local.env.example). Leaving PAM_OIDC_ISSUER unset keeps OIDC
# fully disabled, preserving the previous startup behaviour.
if [[ -n "${PAM_OIDC_ISSUER:-}" ]]; then
  : "${PAM_OIDC_CLIENT_ID:?PAM_OIDC_CLIENT_ID is required when PAM_OIDC_ISSUER is set}"
  oidc_secret_file="${PAM_OIDC_CLIENT_SECRET_FILE:-$ROOT_DIR/.secrets/oidc_client_secret}"
  if [[ ! -f "$oidc_secret_file" ]]; then
    echo "missing OIDC client secret file: $oidc_secret_file" >&2
    echo "write the client secret as a single line, with no quotes or trailing blank lines" >&2
    exit 1
  fi
  # The redirect URI must match the IdP's exact whitelist byte-for-byte. The
  # default targets the Vite dev origin on :8081 (which proxies /api to
  # pam-control on PAM_PORT); landing the callback directly on pam-control
  # would 302 to / with no SPA to serve it.
  oidc_redirect_url="${PAM_OIDC_REDIRECT_URL:-http://127.0.0.1:8081/api/v1/auth/oidc/callback}"
  CONTROL_ENV+=(
    PAM_OIDC_ISSUER="$PAM_OIDC_ISSUER"
    PAM_OIDC_CLIENT_ID="$PAM_OIDC_CLIENT_ID"
    PAM_OIDC_CLIENT_SECRET_FILE="$oidc_secret_file"
    PAM_OIDC_REDIRECT_URL="$oidc_redirect_url"
    PAM_OIDC_DEFAULT_TENANT_ID="${PAM_OIDC_DEFAULT_TENANT_ID:-platform}"
    PAM_OIDC_DEFAULT_ROLE="${PAM_OIDC_DEFAULT_ROLE:-user}"
  )
  echo "OIDC enabled: issuer=$PAM_OIDC_ISSUER redirect=$oidc_redirect_url default_role=${PAM_OIDC_DEFAULT_ROLE:-user}"
else
  echo "OIDC disabled (export PAM_OIDC_ISSUER to enable; see scripts/oidc-local.env.example)"
fi

if [[ -f "$ROOT_DIR/.secrets/dingtalk_webhook" && -f "$ROOT_DIR/.secrets/dingtalk_secret" ]]; then
  CONTROL_ENV+=(PAM_DINGTALK_WEBHOOK_FILE="$ROOT_DIR/.secrets/dingtalk_webhook")
  CONTROL_ENV+=(PAM_DINGTALK_SECRET_FILE="$ROOT_DIR/.secrets/dingtalk_secret")
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
  CONTROL_ENV+=(PAM_DINGTALK_APP_KEY_FILE="$ROOT_DIR/.secrets/dingtalk_app_key")
  CONTROL_ENV+=(PAM_DINGTALK_APP_SECRET_FILE="$ROOT_DIR/.secrets/dingtalk_app_secret")
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
  CONTROL_ENV+=(PAM_DINGTALK_CARD_TEMPLATE_ID_FILE="$ROOT_DIR/.secrets/dingtalk_card_template_id")
  CONTROL_ENV+=(PAM_DINGTALK_OPEN_CONVERSATION_ID_FILE="$ROOT_DIR/.secrets/dingtalk_open_conversation_id")
  CONTROL_ENV+=(PAM_DINGTALK_ROBOT_CODE_FILE="$ROOT_DIR/.secrets/dingtalk_robot_code")
elif [[ "$card_count" -ne 0 ]]; then
  echo "dingtalk_card_template_id, dingtalk_open_conversation_id and dingtalk_robot_code must all exist under $ROOT_DIR/.secrets" >&2
  exit 1
fi

# Foreground mode is useful when the caller already owns process supervision
# (e.g. a dev task runner), where a detached nohup child would be reaped as
# soon as the launching shell exits.
if [[ "${PAM_FOREGROUND:-}" == "1" ]]; then
  exec env "${CONTROL_ENV[@]}" "$ROOT_DIR/server/bin/pam-control"
fi

nohup env "${CONTROL_ENV[@]}" \
  "$ROOT_DIR/server/bin/pam-control" \
  >"$LOG_DIR/pam-control-direct.log" 2>"$LOG_DIR/pam-control-direct.err" </dev/null &

for _ in {1..20}; do
  if lsof -tiTCP:"$CONTROL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "pam-control is listening on :$CONTROL_PORT"
    exit 0
  fi
  sleep 0.25
done

echo "pam-control failed to start; inspect $LOG_DIR/pam-control-direct.err" >&2
tail -n 40 "$LOG_DIR/pam-control-direct.err" >&2 || true
exit 1
