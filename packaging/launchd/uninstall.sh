#!/bin/bash
set -euo pipefail

# StealthScale macOS uninstaller — clean removal.
# Usage:
#   sudo ./packaging/launchd/uninstall.sh [--purge]
# or via CLI:
#   sudo stscale uninstall [--purge]
#
# Without --purge: removes service + binary, keeps config/state/logs.
# With --purge: also removes /usr/local/etc/stealthscale, /Library/Application Support/stealthscale, /Library/Logs/stealthscale

PLIST_DST="/Library/LaunchDaemons/com.stealthscale.plist"
PURGE=0
if [[ "${1:-}" == "--purge" ]]; then PURGE=1; fi

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo $0 [--purge]"
  exit 1
fi

echo "[*] Uninstalling StealthScale (macOS) purge=$PURGE"

echo "[*] Unloading $PLIST_DST (if loaded)"
launchctl unload "$PLIST_DST" 2>/dev/null || true
launchctl bootout "system/$PLIST_DST" 2>/dev/null || true
if [[ -f "$PLIST_DST" ]]; then
  rm -f "$PLIST_DST"
  echo "[*] removed $PLIST_DST"
fi

for bin in "/usr/local/bin/stscale" "/opt/homebrew/bin/stscale"; do
  if [[ -f "$bin" ]]; then
    rm -f "$bin"
    echo "[*] removed $bin"
  fi
done

if [[ $PURGE -eq 1 ]]; then
  for p in "/usr/local/etc/stealthscale" "/Library/Application Support/stealthscale" "/Library/Logs/stealthscale" "/var/run/stealthscale"; do
    if [[ -e "$p" ]]; then
      echo "[*] removing $p"
      rm -rf "$p"
    fi
  done
  echo "[*] purge complete"
else
  echo "[*] kept /usr/local/etc/stealthscale and /Library/Application Support/stealthscale (use --purge to delete)"
  echo "[*] kept /Library/Logs/stealthscale"
fi

echo "[*] Done. Brew alternative: brew uninstall stealthscale (if installed via brew)"
