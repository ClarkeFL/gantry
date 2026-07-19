#!/bin/sh
# gantry installer — Ubuntu/Debian, run as root:
#   curl -fsSL https://raw.githubusercontent.com/ClarkeFL/gantry/main/install.sh | sudo GANTRY_REPO=ClarkeFL/gantry sh
set -e

REPO="${GANTRY_REPO:?set GANTRY_REPO=youruser/gantry}"
DOKKU_TAG="${DOKKU_TAG:-v0.35.20}"

[ "$(id -u)" = 0 ] || { echo "run as root (sudo)"; exit 1; }

# 0. base system: refresh package lists, apply pending updates, ensure tools
echo "==> updating system packages..."
export DEBIAN_FRONTEND=noninteractive
apt-get -qq update
apt-get -y -qq upgrade
apt-get -y -qq install curl wget ca-certificates unattended-upgrades

# automatic OS security updates
printf 'APT::Periodic::Update-Package-Lists "1";\nAPT::Periodic::Unattended-Upgrade "1";\n' \
  > /etc/apt/apt.conf.d/20auto-upgrades

# 1. dokku (skipped if already installed)
if ! command -v dokku >/dev/null 2>&1; then
  echo "==> installing dokku $DOKKU_TAG (this takes a few minutes)..."
  wget -qO /tmp/dokku-bootstrap.sh "https://dokku.com/install/$DOKKU_TAG/bootstrap.sh"
  DOKKU_TAG="$DOKKU_TAG" bash /tmp/dokku-bootstrap.sh
fi

# 1b. letsencrypt plugin for one-click SSL (skipped if present)
dokku plugin:installed letsencrypt >/dev/null 2>&1 || dokku plugin:install https://github.com/dokku/dokku-letsencrypt.git

# 2. gantry binary from latest GitHub release
ARCH="$(uname -m)"
case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
echo "==> downloading gantry ($ARCH)..."
curl -fsSL "https://github.com/$REPO/releases/latest/download/gantry-linux-$ARCH" -o /usr/local/bin/gantry.new
chmod +x /usr/local/bin/gantry.new
mv /usr/local/bin/gantry.new /usr/local/bin/gantry

# 3. account setup happens in the browser on first visit (register → enable 2FA)
mkdir -p /var/lib/gantry

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

IP=$(hostname -I 2>/dev/null | awk '{print $1}')
echo ""
echo "================================================="
echo "  gantry is installed and running"
echo ""
echo "  panel:    https://$IP:8022"
echo "            (self-signed certificate — your browser warns once; accept to continue)"
echo "  service:  systemctl status gantry"
echo "  logs:     journalctl -u gantry -f"
echo ""
echo "  NEXT: open the panel URL above, create your"
echo "  admin account, then enable 2FA in Settings."
echo "================================================="
