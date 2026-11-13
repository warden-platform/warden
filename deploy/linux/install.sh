#!/usr/bin/env bash
set -euo pipefail

CONTROL_URL=""
ALLOW_INSECURE_HTTP=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --control-url) CONTROL_URL="${2:-}"; shift 2 ;;
    --allow-insecure-http) ALLOW_INSECURE_HTTP=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$CONTROL_URL" ]]; then
  echo "--control-url is required" >&2
  exit 2
fi
if [[ "$CONTROL_URL" =~ [[:space:]] ]] || [[ "$CONTROL_URL" == *\"* ]] || [[ "$CONTROL_URL" == *\\* ]]; then
  echo "--control-url contains unsafe characters" >&2
  exit 2
fi
case "$CONTROL_URL" in
  https://*) ;;
  http://127.0.0.1:*|http://localhost:*) ;;
  http://*)
    if [[ "$ALLOW_INSECURE_HTTP" != true ]]; then
      echo "HTTP control URLs require --allow-insecure-http; production must use HTTPS" >&2
      exit 2
    fi
    ;;
  *) echo "--control-url must be an absolute HTTP(S) URL" >&2; exit 2 ;;
esac
if [[ "$(id -u)" -ne 0 ]]; then
  echo "installer must run as root" >&2
  exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemd is required" >&2
  exit 1
fi
if ! command -v sha256sum >/dev/null 2>&1; then
  echo "sha256sum is required" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

install -d -m 0750 /etc/pam-linux /etc/pam-linux/credentials /var/lib/pam-linux
if ! id pam-linux >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/pam-linux --shell /usr/sbin/nologin pam-linux
fi

INSTALL_TMP=$(mktemp -d /usr/local/bin/.pam-linux-install.XXXXXX)
CONNECTOR_TMP="$INSTALL_TMP/pam-linux-connector-linux-${ARCH}"
HELPER_TMP="$INSTALL_TMP/pam-linux-helper-linux-${ARCH}"
UNINSTALL_TMP="$INSTALL_TMP/uninstall-linux.sh"
cleanup() {
  rm -rf "$INSTALL_TMP"
}
trap cleanup EXIT

curl -fsSL "${CONTROL_URL%/}/downloads/pam-linux-connector-linux/${ARCH}" -o "$CONNECTOR_TMP"
curl -fsSL "${CONTROL_URL%/}/downloads/pam-linux-helper-linux/${ARCH}" -o "$HELPER_TMP"
curl -fsSL "${CONTROL_URL%/}/downloads/uninstall-linux.sh" -o "$UNINSTALL_TMP"
curl -fsSL "${CONTROL_URL%/}/downloads/SHA256SUMS" -o "$INSTALL_TMP/SHA256SUMS"
grep -E "  (pam-linux-connector-linux-${ARCH}|pam-linux-helper-linux-${ARCH}|uninstall-linux.sh)$" \
  "$INSTALL_TMP/SHA256SUMS" >"$INSTALL_TMP/SHA256SUMS.selected"
if [[ "$(wc -l <"$INSTALL_TMP/SHA256SUMS.selected" | tr -d ' ')" != "3" ]]; then
  echo "release checksum manifest is incomplete" >&2
  exit 1
fi
(cd "$INSTALL_TMP" && sha256sum --check SHA256SUMS.selected)
chmod 0755 "$CONNECTOR_TMP" "$HELPER_TMP" "$UNINSTALL_TMP"

systemctl stop pam-linux-connector.service 2>/dev/null || true
systemctl stop pam-linux-helper.service 2>/dev/null || true
mv -f "$CONNECTOR_TMP" /usr/local/bin/pam-linux-connector
mv -f "$HELPER_TMP" /usr/local/bin/pam-linux-helper
mv -f "$UNINSTALL_TMP" /usr/local/sbin/pam-linux-uninstall
chown -R pam-linux:pam-linux /var/lib/pam-linux
chown root:pam-linux /etc/pam-linux/credentials
if getent group systemd-journal >/dev/null 2>&1; then
  usermod -a -G systemd-journal pam-linux
fi

cat >/etc/systemd/system/pam-linux-helper.service <<EOF
[Unit]
Description=PAM Linux Privileged Read-Only Helper
After=docker.service

[Service]
Type=simple
User=root
Group=pam-linux
RuntimeDirectory=pam-linux
RuntimeDirectoryMode=0750
ExecStart=/usr/local/bin/pam-linux-helper
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
RestrictAddressFamilies=AF_UNIX

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/pam-linux-connector.service <<EOF
[Unit]
Description=PAM Linux Connector
After=network-online.target
After=pam-linux-helper.service
Wants=network-online.target
Wants=pam-linux-helper.service

[Service]
Type=simple
User=pam-linux
Group=pam-linux
Environment="PAM_CONTROL_URL=${CONTROL_URL%/}"
Environment=PAM_LINUX_STATE_FILE=/var/lib/pam-linux/state.json
ExecStart=/usr/local/bin/pam-linux-connector
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/pam-linux

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable pam-linux-helper.service
systemctl restart pam-linux-helper.service
systemctl enable pam-linux-connector.service
systemctl restart pam-linux-connector.service
echo "pam-linux-connector installed; run: journalctl -u pam-linux-connector -f"
