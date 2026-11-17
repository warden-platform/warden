#!/usr/bin/env bash
# Generate the four internal signing/encryption keys once. User-provided
# credentials belong in .env; these internal keys are never edited by hand.
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SECRET_FILE="${WARDEN_INTERNAL_SECRETS_FILE:-$ROOT_DIR/var/warden/secrets.json}"

if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl is required to initialize local secrets" >&2
  exit 1
fi
if [[ -e "$SECRET_FILE" ]]; then
  echo "internal secrets already exist; refusing to overwrite: $SECRET_FILE" >&2
  exit 1
fi

umask 077
mkdir -p "$(dirname "$SECRET_FILE")"
temporary="$SECRET_FILE.tmp.$$"
trap '[[ -e "$temporary" ]] && rm -f "$temporary"' EXIT
{
  printf '{\n'
  keys=(WARDEN_ADMIN_API_KEY WARDEN_TASK_SIGNING_KEY WARDEN_JWT_SIGNING_KEY WARDEN_CREDENTIAL_ENCRYPTION_KEY)
  for i in "${!keys[@]}"; do
    comma=','
    [[ $i -eq $((${#keys[@]} - 1)) ]] && comma=''
    printf '  "%s": "%s"%s\n' "${keys[$i]}" "$(openssl rand -hex 32)" "$comma"
  done
  printf '}\n'
} > "$temporary"
mv "$temporary" "$SECRET_FILE"
trap - EXIT
chmod 600 "$SECRET_FILE"
echo "initialized internal secrets: $SECRET_FILE"