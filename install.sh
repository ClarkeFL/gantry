#!/bin/sh
# gantry installer — Ubuntu/Debian, run as root:
#   curl -fsSL https://raw.githubusercontent.com/ClarkeFL/gantry/main/install.sh | sudo GANTRY_REPO=ClarkeFL/gantry sh
set -e

REPO="${GANTRY_REPO:?set GANTRY_REPO=youruser/gantry}"
DOKKU_TAG="${DOKKU_TAG:-v0.35.20}"

[ "$(id -u)" = 0 ] || { echo "run as root (sudo)"; exit 1; }

# 1. dokku (skipped if already installed)
if ! command -v dokku >/dev/null 2>&1; then
  echo "==> installing dokku $DOKKU_TAG (this takes a few minutes)..."
  wget -qO /tmp/dokku-bootstrap.sh "https://dokku.com/install/$DOKKU_TAG/bootstrap.sh"
  DOKKU_TAG="$DOKKU_TAG" bash /tmp/dokku-bootstrap.sh
fi

# 2. gantry binary from latest GitHub release
ARCH="$(uname -m)"
case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
echo "==> downloading gantry ($ARCH)..."
curl -fsSL "https://github.com/$REPO/releases/latest/download/gantry-linux-$ARCH" -o /usr/local/bin/gantry.new
chmod +x /usr/local/bin/gantry.new
mv /usr/local/bin/gantry.new /usr/local/bin/gantry

# 3. first-run setup (password + 2FA secret) — interactive, skipped if done before
mkdir -p /var/lib/gantry
if [ ! -f /var/lib/gantry/auth.json ]; then
  if [ -t 0 ]; then
    gantry init
  elif (exec < /dev/tty) 2>/dev/null; then
    gantry init < /dev/tty   # curl|sh: stdin is the script, prompt on the tty instead
  else
    echo "!! no tty — run 'gantry init' then 'systemctl restart gantry' to finish setup"
  fi
fi

# 4. systemd service — auto-start on boot, auto-restart (also how self-update applies)
cat > /etc/systemd/system/gantry.service <<EOF
[Unit]
Description=gantry — dokku panel
After=network.target

[Service]
Environment=GANTRY_ADDR=:8022
Environment=GANTRY_REPO=$REPO
ExecStart=/usr/local/bin/gantry
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now gantry

echo ""
echo "==> gantry is running on http://$(hostname -I 2>/dev/null | awk '{print $1}'):8022"
echo "    tip: put it behind https, e.g. deploy nothing and just:"
echo "    dokku domains + letsencrypt on a proxy app, or a Cloudflare tunnel."
