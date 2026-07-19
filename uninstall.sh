#!/bin/sh
# gantry uninstaller, removes the panel, leaves dokku and your apps alone.
#   sudo sh uninstall.sh          # remove panel, keep data (/var/lib/gantry)
#   sudo sh uninstall.sh --purge  # remove panel AND its data + panel-managed cron jobs
set -e

[ "$(id -u)" = 0 ] || { echo "run as root (sudo)"; exit 1; }

systemctl disable --now gantry 2>/dev/null || true
rm -f /etc/systemd/system/gantry.service
systemctl daemon-reload
rm -f /usr/local/bin/gantry

if [ "$1" = "--purge" ]; then
  rm -f /etc/cron.d/gantry-*
  rm -rf /var/lib/gantry
  echo "gantry removed, including its data and cron jobs."
else
  echo "gantry removed. Kept: /var/lib/gantry (account, categories, cron config)"
  echo "and /etc/cron.d/gantry-* (your scheduled jobs keep running)."
  echo "Run with --purge to remove those too."
fi
echo ""
echo "dokku and your apps are untouched. To remove dokku as well:"
echo "  apt-get purge dokku herokuish && rm -rf /var/lib/dokku"
