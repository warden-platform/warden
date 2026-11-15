#!/bin/sh
set -eu

usage() {
  echo "usage: $0 <multica-agent-id> <warden-api-endpoint> <warden-agent-api-key-file>" >&2
  exit 2
}

[ "$#" -eq 3 ] || usage
agent_id=$1
endpoint=$2
key_file=$3

command -v multica >/dev/null 2>&1 || { echo "multica CLI is required" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }
[ -r "$key_file" ] || { echo "API Key file is not readable" >&2; exit 1; }

current_file=$(mktemp "${TMPDIR:-/tmp}/warden-multica-env-current.XXXXXX")
merged_file=$(mktemp "${TMPDIR:-/tmp}/warden-multica-env-merged.XXXXXX")
trap 'rm -f "$current_file" "$merged_file"' EXIT HUP INT TERM
chmod 600 "$current_file" "$merged_file"

# env set replaces the whole custom_env object. Existing names are retained
# with the CLI's **** sentinel, so their values never enter the generated file.
multica agent env get "$agent_id" --output json >"$current_file"
jq --arg endpoint "$endpoint" --rawfile api_key "$key_file" '
  if type != "object" then error("custom_env response is not a JSON object") else . end
  | with_entries(.value = "****")
  | .WARDEN_API_ENDPOINT = $endpoint
  | .WARDEN_AGENT_API_KEY = ($api_key | gsub("[\\r\\n]+$"; ""))
  | if .WARDEN_AGENT_API_KEY == "" then error("API Key file is empty") else . end
' "$current_file" >"$merged_file"

multica agent env set "$agent_id" --custom-env-file "$merged_file" --output json >/dev/null
echo "Warden endpoint and Agent API Key configured for Multica Agent $agent_id"
