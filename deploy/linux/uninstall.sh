#!/usr/bin/env bash
set -euo pipefail
if [[ "$(id -u)" -ne 0 ]]; then echo "uninstaller must run as root" >&2; exit 1; fi
systemctl disable --now pam-linux-connector.service pam-linux-helper.service 2>/dev/null || true
rm -f /etc/systemd/system/pam-linux-connector.service /etc/systemd/system/pam-linux-helper.service
rm -f /usr/local/bin/pam-linux-connector /usr/local/bin/pam-linux-helper /usr/local/sbin/pam-linux-uninstall
rm -rf /etc/pam-linux /var/lib/pam-linux
systemctl daemon-reload
