#!/usr/bin/env bash
set -euo pipefail

CONTROL_URL=""
CONNECTOR_NAME=""
ENVIRONMENT="production"
TASK_KEY_FILE="/etc/pam-network/task-signing-key"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --control-url) CONTROL_URL="${2:?missing value}"; shift 2 ;;
    --name) CONNECTOR_NAME="${2:?missing value}"; shift 2 ;;
    --environment) ENVIRONMENT="${2:?missing value}"; shift 2 ;;
    --task-key-file) TASK_KEY_FILE="${2:?missing value}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$CONTROL_URL" && -n "$CONNECTOR_NAME" ]] || { echo "usage: $0 --control-url URL --name NAME [--environment production] [--task-key-file PATH]" >&2; exit 2; }
[[ "$CONTROL_URL" != *[\"\'\\$'\n\r']* && "$CONNECTOR_NAME" != *[\"\'\\$'\n\r']* ]] || { echo "control URL or connector name contains unsafe characters" >&2; exit 2; }
[[ -r "$TASK_KEY_FILE" ]] || { echo "task signing key file is missing: $TASK_KEY_FILE" >&2; exit 1; }
ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) echo "unsupported architecture" >&2; exit 1;; esac
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
ARTIFACT="pam-network-connector-linux-${ARCH}"
curl -fsSL "${CONTROL_URL%/}/downloads/pam-network-connector-linux/${ARCH}" -o "$TMP/$ARTIFACT"
curl -fsSL "${CONTROL_URL%/}/downloads/SHA256SUMS" -o "$TMP/SHA256SUMS"
grep -E "  pam-network-connector-linux-${ARCH}$" "$TMP/SHA256SUMS" >"$TMP/SHA256SUMS.selected"
[[ "$(wc -l <"$TMP/SHA256SUMS.selected" | tr -d ' ')" == "1" ]] || { echo "release checksum manifest is incomplete" >&2; exit 1; }
(cd "$TMP" && sha256sum --check SHA256SUMS.selected)
getent group pam-network >/dev/null 2>&1 || groupadd --system pam-network
id pam-network >/dev/null 2>&1 || useradd --system --gid pam-network --home-dir /var/lib/pam-network --shell /usr/sbin/nologin pam-network
install -d -m 0750 -o pam-network -g pam-network /var/lib/pam-network
install -d -m 0750 -o root -g pam-network /var/lib/pam-network/credentials
install -d -m 0750 -o root -g pam-network /etc/pam-network
install -o root -g root -m 0755 "$TMP/$ARTIFACT" /usr/local/bin/pam-network-connector
install -o root -g pam-network -m 0640 "$TASK_KEY_FILE" /etc/pam-network/task-signing-key
cat >/etc/systemd/system/pam-network-connector.service <<EOF
[Unit]
Description=PAM H3C Network Connector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pam-network
Group=pam-network
Environment="PAM_CONTROL_URL=${CONTROL_URL%/}"
Environment="PAM_CONNECTOR_NAME=${CONNECTOR_NAME}"
Environment="PAM_CONNECTOR_ENVIRONMENT=${ENVIRONMENT}"
Environment="PAM_TASK_SIGNING_KEY_FILE=/etc/pam-network/task-signing-key"
Environment="PAM_NETWORK_CREDENTIAL_DIR=/var/lib/pam-network/credentials"
ExecStart=/usr/local/bin/pam-network-connector
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/pam-network
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now pam-network-connector.service
echo "pam-network-connector installed; run: journalctl -u pam-network-connector -f"
