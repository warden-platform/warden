#!/usr/bin/env bash
set -euo pipefail

endpoint=${PAM_API_ENDPOINT:-}
key=${PAM_REGISTRATION_API_KEY:-}

fail() {
  echo "pam-registration: $*" >&2
  exit 1
}

require_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$ ]] ||
    fail "$2 must be a UUID"
}

[[ -n "$endpoint" ]] || fail "PAM_API_ENDPOINT is required"
[[ "$endpoint" =~ ^https?://[^/]+/api/v1/?$ ]] || fail "PAM_API_ENDPOINT must be an absolute PAM /api/v1 endpoint"
endpoint=${endpoint%/}
case "$endpoint" in
  http://localhost:*|http://127.0.0.1:*|https://*) ;;
  *) fail "non-local PAM_API_ENDPOINT must use HTTPS" ;;
esac
[[ "$key" == pam_rgk_* ]] || fail "PAM_REGISTRATION_API_KEY must be a pam_rgk_ registration credential"

api() {
  local method=$1 path=$2 body_file=${3:-}
  local args=(--fail-with-body --silent --show-error --request "$method"
    --header "Authorization: Bearer $key" --header 'Accept: application/json')
  if [[ -n "$body_file" ]]; then
    [[ -f "$body_file" ]] || fail "request file not found: $body_file"
    args+=(--header 'Content-Type: application/json' --data-binary "@$body_file")
  fi
  curl "${args[@]}" "$endpoint$path"
}

command=${1:-}
case "$command" in
  preflight)
    api GET /connectors >/dev/null
    api GET /projects >/dev/null
    printf '{"status":"ready","endpoint":"%s"}\n' "$endpoint"
    ;;
  list-connectors)
    api GET /connectors
    ;;
  list-projects)
    api GET /projects
    ;;
  list-integrations)
    api GET /integrations
    ;;
  list-connector-integrations)
    [[ $# -eq 2 ]] || fail "usage: $0 list-connector-integrations CONNECTOR_ID"
    require_uuid "$2" "CONNECTOR_ID"
    api GET "/connectors/$2/integrations"
    ;;
  create-project)
    [[ $# -ge 2 && $# -le 3 ]] || fail "usage: $0 create-project NAME [DESCRIPTION]"
    request=$(mktemp)
    trap 'rm -f "$request"' EXIT
    jq -n --arg name "$2" --arg description "${3:-Created by PAM registration bot}" \
      '{name:$name,description:$description}' >"$request"
    api POST /projects "$request"
    ;;
  update-project)
    [[ $# -ge 3 && $# -le 4 ]] || fail "usage: $0 update-project PROJECT_ID NAME [DESCRIPTION]"
    require_uuid "$2" "PROJECT_ID"
    request=$(mktemp)
    trap 'rm -f "$request"' EXIT
    jq -n --arg name "$3" --arg description "${4:-Updated by PAM registration bot}" \
      '{name:$name,description:$description}' >"$request"
    api PUT "/projects/$2" "$request"
    ;;
  create-integration)
    [[ $# -eq 3 ]] || fail "usage: $0 create-integration CONNECTOR_ID REQUEST_JSON_FILE"
    require_uuid "$2" "CONNECTOR_ID"
    api POST "/connectors/$2/integrations" "$3"
    ;;
  update-integration)
    [[ $# -eq 4 ]] || fail "usage: $0 update-integration CONNECTOR_ID INTEGRATION_ID REQUEST_JSON_FILE"
    require_uuid "$2" "CONNECTOR_ID"
    require_uuid "$3" "INTEGRATION_ID"
    api PUT "/connectors/$2/integrations/$3" "$4"
    ;;
  get-project-scope)
    [[ $# -eq 2 ]] || fail "usage: $0 get-project-scope INTEGRATION_ID"
    require_uuid "$2" "INTEGRATION_ID"
    api GET "/integrations/$2/project-scope"
    ;;
  set-project-scope)
    [[ $# -eq 3 ]] || fail "usage: $0 set-project-scope INTEGRATION_ID REQUEST_JSON_FILE"
    require_uuid "$2" "INTEGRATION_ID"
    api PUT "/integrations/$2/project-scope" "$3"
    ;;
  *)
    fail "supported commands: preflight, list-connectors, list-projects, list-integrations, list-connector-integrations, create-project, update-project, create-integration, update-integration, get-project-scope, set-project-scope"
    ;;
esac
