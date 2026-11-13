#!/usr/bin/env bash
set -euo pipefail

CONTROL_URL="https://pam-admin.example.com"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --control-url) CONTROL_URL="${2:-}"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

case "$CONTROL_URL" in
  https://*) ;;
  *) echo "PAM control URL must use HTTPS" >&2; exit 2 ;;
esac
if [[ "$CONTROL_URL" =~ [[:space:]] ]] || [[ "$CONTROL_URL" == *\"* ]] || [[ "$CONTROL_URL" == *\\* ]]; then
  echo "PAM control URL contains unsafe characters" >&2
  exit 2
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

case "$(uname -s)" in
  Darwin)
    PLATFORM=macos
    ARTIFACT="pam-host-connector-macos-${ARCH}"
    ;;
  MINGW*|MSYS*|CYGWIN*)
    PLATFORM=windows
    if [[ "$ARCH" != amd64 ]]; then
      echo "this release supports Windows amd64 only" >&2
      exit 1
    fi
    ARTIFACT="pam-host-connector-windows-amd64.exe"
    ;;
  *)
    echo "supported systems: Windows amd64 (Git Bash), macOS amd64, macOS arm64" >&2
    exit 1
    ;;
esac

INSTALL_TMP=$(mktemp -d "${TMPDIR:-/tmp}/pam-host-install.XXXXXX")
cleanup() { rm -rf "$INSTALL_TMP"; }
trap cleanup EXIT

curl -fsSL "${CONTROL_URL%/}/downloads/pam-host-connector/${PLATFORM}/${ARCH}" -o "$INSTALL_TMP/$ARTIFACT"
curl -fsSL "${CONTROL_URL%/}/downloads/SHA256SUMS" -o "$INSTALL_TMP/SHA256SUMS"
EXPECTED=$(awk -v artifact="$ARTIFACT" '$2 == artifact {print $1}' "$INSTALL_TMP/SHA256SUMS")
if [[ -z "$EXPECTED" ]]; then
  echo "release checksum manifest is missing $ARTIFACT" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$INSTALL_TMP/$ARTIFACT" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$INSTALL_TMP/$ARTIFACT" | awk '{print $1}')
fi
if [[ "$ACTUAL" != "$EXPECTED" ]]; then
  echo "checksum verification failed for $ARTIFACT" >&2
  exit 1
fi

if [[ "$PLATFORM" == macos ]]; then
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "macOS installation requires root; run: curl -fsSL ${CONTROL_URL%/}/downloads/install-host.sh | sudo bash" >&2
    exit 1
  fi
  install -d -m 0755 /usr/local/libexec/pam-host
  install -d -m 0700 /var/lib/pam-host
  install -m 0755 "$INSTALL_TMP/$ARTIFACT" /usr/local/libexec/pam-host/pam-host-connector
  touch /var/log/pam-host-connector.log
  PAM_USER=nobody
  PAM_GROUP=$(id -gn "$PAM_USER")
  chown "$PAM_USER:$PAM_GROUP" /var/lib/pam-host /var/log/pam-host-connector.log
  cat >/Library/LaunchDaemons/io.pam.host-connector.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>io.pam.host-connector</string>
  <key>ProgramArguments</key><array><string>/usr/local/libexec/pam-host/pam-host-connector</string></array>
  <key>UserName</key><string>nobody</string>
  <key>EnvironmentVariables</key><dict>
    <key>PAM_CONTROL_URL</key><string>${CONTROL_URL%/}</string>
    <key>PAM_HOST_STATE_FILE</key><string>/var/lib/pam-host/state.json</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/pam-host-connector.log</string>
  <key>StandardErrorPath</key><string>/var/log/pam-host-connector.log</string>
</dict></plist>
EOF
  chown root:wheel /Library/LaunchDaemons/io.pam.host-connector.plist
  chmod 0644 /Library/LaunchDaemons/io.pam.host-connector.plist
  # A previous installation may still be loaded.  `bootout` returns before
  # launchd has finished tearing the job down, so bootstrapping immediately
  # can fail with status 37 (Operation already in progress).  Wait until the
  # old job is gone, then retry bootstrap briefly to make reinstalls safe.
  LABEL=io.pam.host-connector
  launchctl bootout "system/$LABEL" >/dev/null 2>&1 || true
  for _ in {1..20}; do
    if ! launchctl print "system/$LABEL" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
  bootstrapped=0
  for _ in {1..10}; do
    if launchctl bootstrap system /Library/LaunchDaemons/io.pam.host-connector.plist >/dev/null 2>&1; then
      bootstrapped=1
      break
    fi
    sleep 0.5
  done
  if [[ "$bootstrapped" -ne 1 ]]; then
    echo "failed to load PAM Host Connector LaunchDaemon" >&2
    exit 1
  fi
  echo "PAM Host Connector installed for macOS ${ARCH}."
  echo "Pairing code: sudo tail -f /var/log/pam-host-connector.log"
  exit 0
fi

if ! command -v powershell.exe >/dev/null 2>&1; then
  echo "Windows installation must run from Git Bash with PowerShell available" >&2
  exit 1
fi
IS_ADMIN=$(powershell.exe -NoProfile -NonInteractive -Command '([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)' | tr -d '\r')
if [[ "$IS_ADMIN" != True ]]; then
  echo "run Git Bash as Administrator, then execute this installer again" >&2
  exit 1
fi

WIN_SOURCE=$(cygpath -w "$INSTALL_TMP/$ARTIFACT")
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "
  \$ErrorActionPreference = 'Stop'
  \$installDir = 'C:\Program Files\PAM'
  \$stateDir = 'C:\ProgramData\pam-host'
  New-Item -ItemType Directory -Force \$installDir, \$stateDir | Out-Null
  Copy-Item -Force '$WIN_SOURCE' (Join-Path \$installDir 'pam-host-connector.exe')
  \$runnerPath = Join-Path \$stateDir 'run.ps1'
  \$runner = @'
\$env:PAM_CONTROL_URL = '${CONTROL_URL%/}'
\$env:PAM_HOST_STATE_FILE = 'C:\ProgramData\pam-host\state.json'
& 'C:\Program Files\PAM\pam-host-connector.exe' *>> 'C:\ProgramData\pam-host\connector.log'
'@
  Set-Content -Path \$runnerPath -Value \$runner -Encoding UTF8
  icacls.exe \$stateDir /inheritance:r /grant:r 'SYSTEM:(OI)(CI)(F)' 'Administrators:(OI)(CI)(F)' 'LOCAL SERVICE:(OI)(CI)(M)' | Out-Null
  \$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "C:\ProgramData\pam-host\run.ps1"'
  \$trigger = New-ScheduledTaskTrigger -AtStartup
  \$principal = New-ScheduledTaskPrincipal -UserId 'NT AUTHORITY\LOCAL SERVICE' -LogonType ServiceAccount -RunLevel Limited
  Register-ScheduledTask -TaskName 'PAM Host Connector' -Action \$action -Trigger \$trigger -Principal \$principal -Description 'PAM Windows Host Connector' -Force | Out-Null
  Start-ScheduledTask -TaskName 'PAM Host Connector'
"
echo "PAM Host Connector installed for Windows amd64."
echo "State: C:\\ProgramData\\pam-host\\state.json"
echo "Pairing code is written to C:\\ProgramData\\pam-host\\connector.log"
echo "Use PAM Web: Targets -> 接入 Windows 主机, then enter the code from that log."
