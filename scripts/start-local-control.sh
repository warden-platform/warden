#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
LOG_DIR=/tmp

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

existing_pid=$(lsof -tiTCP:8080 -sTCP:LISTEN 2>/dev/null || true)
if [[ -n "$existing_pid" ]]; then
  echo "stopping existing pam-control: $existing_pid"
  kill -TERM $existing_pid 2>/dev/null || true
  for _ in {1..20}; do
    if ! lsof -tiTCP:8080 -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
fi

CONTROL_ENV=(
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

nohup env "${CONTROL_ENV[@]}" \
  "$ROOT_DIR/server/bin/pam-control" \
  >"$LOG_DIR/pam-control-direct.log" 2>"$LOG_DIR/pam-control-direct.err" </dev/null &

for _ in {1..20}; do
  if lsof -tiTCP:8080 -sTCP:LISTEN >/dev/null 2>&1; then
    echo "pam-control is listening on :8080"
    exit 0
  fi
  sleep 0.25
done

echo "pam-control failed to start; inspect $LOG_DIR/pam-control-direct.err" >&2
tail -n 40 "$LOG_DIR/pam-control-direct.err" >&2 || true
exit 1
