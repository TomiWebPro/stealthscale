#!/bin/bash
set -euo pipefail

PLIST_SRC="$(dirname "$0")/com.stealthscale.plist"
PLIST_DST="/Library/LaunchDaemons/com.stealthscale.plist"
BIN_SRC="${1:-./stscale}"
BIN_DST="/usr/local/bin/stscale"
CONFIG_SRC="${2:-./config-example.yaml}"
CONFIG_DST="/usr/local/etc/stealthscale/config.yaml"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo $0 [stscale-binary] [config.yaml]"
  exit 1
fi

echo "[*] Installing stscale binary to $BIN_DST"
install -m 0755 "$BIN_SRC" "$BIN_DST"

echo "[*] Installing config to $CONFIG_DST"
mkdir -p "$(dirname "$CONFIG_DST")"
if [[ -f "$CONFIG_DST" ]]; then
  echo "[*] $CONFIG_DST already exists, not overwriting"
else
  install -m 0644 "$CONFIG_SRC" "$CONFIG_DST"
  echo "[*] Edit $CONFIG_DST before starting"
fi

echo "[*] Creating state and log directories"
mkdir -p "/Library/Application Support/stealthscale"
mkdir -p "/Library/Logs/stealthscale"
mkdir -p "/var/run/stealthscale"
chmod 0750 "/Library/Application Support/stealthscale"

echo "[*] Installing launchd plist to $PLIST_DST"
install -m 0644 "$PLIST_SRC" "$PLIST_DST"

echo "[*] Loading service"
launchctl load "$PLIST_DST" || launchctl bootstrap system "$PLIST_DST" 2>/dev/null || true

echo "[*] Done. Check logs with: log stream --predicate 'process == \"stscale\"' --info"
echo "[*] Or: tail -f /Library/Logs/stealthscale/stealthscale.log"
echo "[*] Manage with: sudo launchctl unload $PLIST_DST  |  sudo launchctl load $PLIST_DST"
