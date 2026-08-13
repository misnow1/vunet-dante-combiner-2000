#!/usr/bin/env bash
# Install combiner lab profile on Raspberry Pi OS / Debian.
# Fail-closed: load drop-forward rules BEFORE enabling IP forwarding.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SITE_YAML="${1:-/etc/combiner/site.yaml}"
I_HAVE_CONSOLE=0
if [[ "${2:-}" == "--i-have-console" ]] || [[ "${1:-}" == "--i-have-console" ]]; then
  I_HAVE_CONSOLE=1
  if [[ "${1:-}" == "--i-have-console" ]]; then
    SITE_YAML="${2:-/etc/combiner/site.yaml}"
  fi
fi

STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT

if [[ "$(id -u)" -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi

if [[ ! -f "$SITE_YAML" ]]; then
  echo "missing site config: $SITE_YAML" >&2
  echo "copy $ROOT/config/site.example.yaml to /etc/combiner/site.yaml and edit" >&2
  exit 1
fi

# SSH over a non-Mgmt path will die when NetworkManager/dhcpcd are torn down.
if [[ -n "${SSH_CONNECTION:-}" && "$I_HAVE_CONSOLE" -ne 1 ]]; then
  echo "refusing install over SSH without --i-have-console" >&2
  echo "this script disables existing network managers and may lock you out" >&2
  echo "run from a serial/HDMI console, or re-run with --i-have-console" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y dnsmasq nftables python3-yaml iproute2 conntrack

modprobe 8021q || true
echo 8021q >/etc/modules-load.d/combiner-8021q.conf

# Avoid conflict with combiner mDNS reflector on udp/5353
systemctl disable --now avahi-daemon 2>/dev/null || true
systemctl mask avahi-daemon 2>/dev/null || true

mkdir -p /etc/combiner
if [[ "$SITE_YAML" != /etc/combiner/site.yaml ]]; then
  cp "$SITE_YAML" /etc/combiner/site.yaml
  SITE_YAML=/etc/combiner/site.yaml
fi
mkdir -p /etc/combiner/allowlists
cp -a "$ROOT/config/allowlists/." /etc/combiner/allowlists/

# Keep forwarding OFF until a validated ruleset is loaded
mkdir -p /etc/sysctl.d
cat >/etc/sysctl.d/99-combiner.conf <<'EOF'
net.ipv4.ip_forward=0
net.ipv4.conf.all.forwarding=0
net.ipv4.conf.default.forwarding=0
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
EOF
sysctl --system >/dev/null

# Stage + validate nftables BEFORE touching live networking
NFT_STAGE="$STAGING/nftables.conf"
python3 "$ROOT/deploy/pi/generate-nftables.py" "$SITE_YAML" "$NFT_STAGE"
if ! nft -c -f "$NFT_STAGE"; then
  echo "generated nftables failed nft -c — aborting (forwarding still off)" >&2
  exit 1
fi

# Fail-closed bootstrap: drop all forward immediately
nft flush ruleset
nft -f - <<'EOF'
table inet combiner_bootstrap {
  chain forward {
    type filter hook forward priority filter; policy drop;
  }
}
EOF

# Backup previous combiner rules if present
if [[ -f /etc/nftables.conf ]]; then
  cp -a /etc/nftables.conf "/etc/nftables.conf.bak.$(date +%s)" || true
fi
cp "$NFT_STAGE" /etc/nftables.conf
nft -f /etc/nftables.conf
conntrack -F 2>/dev/null || true

python3 "$ROOT/deploy/pi/generate-network-config.py" "$SITE_YAML" "$STAGING"

apt-get install -y systemd-resolved || true
systemctl enable systemd-networkd
systemctl disable --now NetworkManager 2>/dev/null || true
systemctl disable --now dhcpcd 2>/dev/null || true

cp -a "$STAGING/systemd/network/." /etc/systemd/network/

mkdir -p /etc/dnsmasq.d
if [[ -f /etc/dnsmasq.conf ]]; then
  sed -i 's/^#\?bind-interfaces.*/bind-interfaces/' /etc/dnsmasq.conf || true
fi
cp "$STAGING/dnsmasq.d/combiner-mgmt.conf" /etc/dnsmasq.d/combiner-mgmt.conf

HOSTNAME="$(python3 -c 'import yaml,sys; print(yaml.safe_load(open(sys.argv[1])).get("hostname") or "combiner")' "$SITE_YAML")"
echo "$HOSTNAME" >/etc/hostname
hostnamectl set-hostname "$HOSTNAME" 2>/dev/null || hostname "$HOSTNAME"

BIN_DIR="$ROOT/bin"
mkdir -p "$BIN_DIR"
if command -v go >/dev/null 2>&1; then
  (cd "$ROOT" && go build -o "$BIN_DIR/combiner" ./cmd/combiner && go build -o "$BIN_DIR/combiner-status" ./cmd/combiner-status)
fi
if [[ ! -x "$BIN_DIR/combiner" ]]; then
  echo "missing $BIN_DIR/combiner — build on a machine with Go and copy binaries" >&2
  exit 1
fi
install -m 0755 "$BIN_DIR/combiner" /usr/local/bin/combiner
install -m 0755 "$BIN_DIR/combiner-status" /usr/local/bin/combiner-status
install -m 0644 "$ROOT/deploy/pi/systemd/combiner.service" /etc/systemd/system/combiner.service

systemctl daemon-reload
systemctl enable nftables dnsmasq combiner
systemctl restart systemd-networkd
systemctl restart nftables
systemctl restart dnsmasq

# Enable forwarding ONLY after rules are live
cat >/etc/sysctl.d/99-combiner.conf <<'EOF'
net.ipv4.ip_forward=1
net.ipv4.conf.all.forwarding=1
net.ipv4.conf.default.forwarding=1
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
EOF
sysctl --system >/dev/null

systemctl restart combiner

echo "Install complete. Run: combiner-status"
echo "Status: http://<mgmt-ip>:8080/  (no DNS on Mgmt by design; use IP)"
