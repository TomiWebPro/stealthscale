#!/bin/bash
set -euo pipefail

# StealthScale systemd uninstaller — clean removal for manual (non-deb) installs.
# Usage:
#   sudo ./packaging/systemd/uninstall.sh [--purge]
# or via CLI:
#   sudo stscale uninstall [--purge]
# or via apt for deb installs:
#   sudo apt remove stealthscale      # keep data
#   sudo apt purge stealthscale       # delete data + user
#
# Without --purge: stops/disables service, removes binary, keeps /etc/stealthscale and /var/lib/stealthscale.
# With --purge: also removes those plus stealthscale user and runtime dir.

PURGE=0
if [[ "${1:-}" == "--purge" ]]; then PURGE=1; fi

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo $0 [--purge]"
  exit 1
fi

echo "[*] Uninstalling StealthScale (systemd) purge=$PURGE"

if [[ -d /run/systemd/system ]]; then
  echo "[*] Stopping stealthscale.service"
  systemctl stop stealthscale.service 2>/dev/null || true
  systemctl disable stealthscale.service 2>/dev/null || true
  systemctl daemon-reload 2>/dev/null || true
fi

for bin in "/usr/bin/stscale" "/usr/local/bin/stscale"; do
  if [[ -f "$bin" ]]; then
    rm -f "$bin"
    echo "[*] removed $bin"
  fi
done

if [[ $PURGE -eq 1 ]]; then
  for p in "/etc/stealthscale" "/var/lib/stealthscale" "/var/lib/coordination" "/var/run/stealthscale" "/run/stealthscale"; do
    if [[ -e "$p" ]]; then
      echo "[*] removing $p"
      rm -rf "$p"
    fi
  done
  if id -u stealthscale >/dev/null 2>&1; then
    echo "[*] removing user stealthscale"
    userdel stealthscale 2>/dev/null || true
  fi
  if id -u coordination >/dev/null 2>&1; then
    userdel coordination 2>/dev/null || true
  fi
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    deb-systemd-helper purge stealthscale.service 2>/dev/null || true
  fi
  echo "[*] purge complete"
else
  echo "[*] kept /etc/stealthscale and /var/lib/stealthscale (use --purge to delete)"
  echo "[*] For deb: sudo apt purge stealthscale to also delete user & data"
fi

echo "[*] Done."
