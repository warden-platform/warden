#!/usr/bin/env bash
set -euo pipefail
if [[ "$(id -u)" -ne 0 ]]; then echo "uninstaller must run as root" >&2; exit 1; fi
systemctl disable --now warden-linux-connector.service warden-linux-helper.service 2>/dev/null || true
rm -f /etc/systemd/system/warden-linux-connector.service /etc/systemd/system/warden-linux-helper.service
rm -f /usr/local/bin/warden-linux-connector /usr/local/bin/warden-linux-helper /usr/local/sbin/warden-linux-uninstall
rm -rf /etc/warden-linux /var/lib/warden-linux
systemctl daemon-reload
