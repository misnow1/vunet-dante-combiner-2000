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
apt-get install -y nftables python3-yaml iproute2 conntrack

DHCP_ENABLED="$(python3 -c 'import yaml,sys; d=yaml.safe_load(open(sys.argv[1])).get("mgmt_dhcp") or {}; print("1" if d.get("enabled", False) else "0")' "$SITE_YAML")"
if [[ "$DHCP_ENABLED" == "1" ]]; then
  apt-get install -y dnsmasq
fi

# VLAN support is mandatory: at least one of Control/Dante is always tagged
# (only the port's single PVID VLAN can be untagged).
if ! modprobe 8021q; then
  echo "cannot load 8021q — kernel has no VLAN support, aborting" >&2
  exit 1
fi
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

# Resolve binaries FIRST: they run the config preflight below, and a missing
# binary must abort before anything is changed rather than after the ruleset,
# networkd units, and NetworkManager have already been rewritten.
BIN_DIR="$ROOT/bin"
mkdir -p "$BIN_DIR"

# A stale bin/combiner validates site.yaml against an OLDER schema and rejects
# configs the current generators accept. Release trees ship no Go sources, so
# their binary is correct by construction; a source checkout may not be.
combiner_needs_build() {
  [[ ! -x "$BIN_DIR/combiner" || ! -x "$BIN_DIR/combiner-status" ]] && return 0
  [[ ! -d "$ROOT/cmd" ]] && return 1
  [[ -n "$(find "$ROOT/cmd" "$ROOT/internal" -name '*.go' -newer "$BIN_DIR/combiner" -print -quit 2>/dev/null)" ]]
}

STALE_BINARY=0
if combiner_needs_build; then
  if command -v go >/dev/null 2>&1; then
    echo "building combiner binaries (missing or older than sources)"
    (cd "$ROOT" && go build -o "$BIN_DIR/combiner" ./cmd/combiner && go build -o "$BIN_DIR/combiner-status" ./cmd/combiner-status)
  elif [[ -x "$BIN_DIR/combiner" ]]; then
    STALE_BINARY=1
  fi
fi

if [[ ! -x "$BIN_DIR/combiner" || ! -x "$BIN_DIR/combiner-status" ]]; then
  echo "missing $BIN_DIR/combiner and/or $BIN_DIR/combiner-status" >&2
  echo "download a release tarball, or build with Go and place binaries in bin/" >&2
  echo "nothing has been changed on this system" >&2
  exit 1
fi

if [[ "$STALE_BINARY" -eq 1 ]]; then
  echo "warning: $BIN_DIR/combiner is older than the Go sources in this checkout" >&2
  echo "         and Go is not installed, so it could not be rebuilt" >&2
fi

# Authoritative preflight: the Go loader is the schema (it rejects unknown keys
# and cross-checks allowlists against the deny floor).
if ! "$BIN_DIR/combiner" -check -config "$SITE_YAML"; then
  echo "site.yaml failed validation — aborting before any change" >&2
  if [[ "$STALE_BINARY" -eq 1 ]]; then
    echo "note: the binary above predates this checkout — rebuild it (make build)" >&2
    echo "      or use a release tarball before trusting this rejection" >&2
  fi
  exit 1
fi

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

# networkd only hands DNS= to systemd-resolved, so install it just for the lab
# uplink case. Installing it rewrites /etc/resolv.conf as a symlink to its stub,
# which leaves the box with no DNS at all if resolved is not actually running.
MGMT_DNS_COUNT="$(python3 -c 'import yaml,sys; v=yaml.safe_load(open(sys.argv[1])).get("vlans") or {}; m=v.get("mgmt") or {}; print(len(m.get("dns") or []))' "$SITE_YAML")"
if [[ "$MGMT_DNS_COUNT" -gt 0 ]]; then
  apt-get install -y systemd-resolved || true
fi
if [[ -e /usr/lib/systemd/system/systemd-resolved.service ]]; then
  systemctl enable --now systemd-resolved || true
fi

systemctl enable systemd-networkd
systemctl disable --now NetworkManager 2>/dev/null || true
systemctl disable --now dhcpcd 2>/dev/null || true

# A surviving network manager silently fights networkd and VLANs never appear.
for svc in NetworkManager dhcpcd; do
  if systemctl is-active --quiet "$svc" 2>/dev/null; then
    echo "$svc is still active and will fight systemd-networkd — aborting" >&2
    echo "stop it manually (systemctl disable --now $svc) and re-run" >&2
    exit 1
  fi
done

mkdir -p /etc/systemd/network
# Drop units from earlier runs first: a leftover .netdev whose Name matches a
# renamed or now-untagged interface keeps networkd from managing that link.
rm -f /etc/systemd/network/*combiner*.netdev /etc/systemd/network/*combiner*.network
cp -a "$STAGING/systemd/network/." /etc/systemd/network/

HOSTNAME="$(python3 -c 'import yaml,sys; print(yaml.safe_load(open(sys.argv[1])).get("hostname") or "combiner")' "$SITE_YAML")"
echo "$HOSTNAME" >/etc/hostname
hostnamectl set-hostname "$HOSTNAME" 2>/dev/null || hostname "$HOSTNAME"

# Binaries were resolved and preflighted near the top of this script.
install -m 0755 "$BIN_DIR/combiner" /usr/local/bin/combiner
install -m 0755 "$BIN_DIR/combiner-status" /usr/local/bin/combiner-status
install -m 0644 "$ROOT/deploy/pi/systemd/combiner.service" /etc/systemd/system/combiner.service

systemctl daemon-reload
systemctl enable nftables combiner
systemctl restart systemd-networkd
networkctl reload 2>/dev/null || true
systemctl restart nftables

# systemd-networkd starting is not proof it configured anything — verify the
# interfaces named in site.yaml actually exist before claiming success.
networkd_diagnostics() {
  echo "--- networkctl list ---" >&2
  networkctl list --no-pager 2>&1 >&2 || true
  echo "--- journalctl -u systemd-networkd (last 40) ---" >&2
  journalctl -u systemd-networkd -b --no-pager -n 40 2>&1 >&2 || true
  echo "--- ip -br addr ---" >&2
  ip -br addr >&2 || true
}

MISSING=""
for _ in $(seq 1 15); do
  MISSING=""
  while read -r role ifname; do
    [[ -z "$ifname" ]] && continue
    if [[ ! -e "/sys/class/net/$ifname" ]]; then
      MISSING+="$role=$ifname "
    fi
  done <"$STAGING/combiner-interfaces.txt"
  [[ -z "$MISSING" ]] && break
  sleep 2
done

if [[ -n "$MISSING" ]]; then
  echo "systemd-networkd did not create: $MISSING" >&2
  echo "forwarding is still OFF; combiner not started" >&2
  echo "if Mgmt should be native/untagged on $(python3 -c 'import yaml,sys; print(yaml.safe_load(open(sys.argv[1]))["physical_interface"])' "$SITE_YAML"), set vlans.mgmt.untagged: true in $SITE_YAML" >&2
  networkd_diagnostics
  exit 1
fi

# Mgmt DHCP: enable dnsmasq only when site.yaml mgmt_dhcp.enabled is true.
if [[ "$(cat "$STAGING/combiner-mgmt-dhcp.enabled")" == "1" ]]; then
  mkdir -p /etc/dnsmasq.d
  if [[ -f /etc/dnsmasq.conf ]]; then
    sed -i 's/^#\?bind-interfaces.*/bind-interfaces/' /etc/dnsmasq.conf || true
  fi
  cp "$STAGING/dnsmasq.d/combiner-mgmt.conf" /etc/dnsmasq.d/combiner-mgmt.conf
  systemctl enable dnsmasq
  systemctl restart dnsmasq
else
  rm -f /etc/dnsmasq.d/combiner-mgmt.conf
  systemctl disable --now dnsmasq 2>/dev/null || true
  echo "mgmt_dhcp.enabled=false — dnsmasq not started"
fi

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

# Type=simple means restart returns 0 even if combiner exits immediately.
sleep 3
if ! systemctl is-active --quiet combiner; then
  echo "combiner failed to stay running:" >&2
  journalctl -u combiner -b --no-pager -n 40 >&2 || true
  exit 1
fi

# A dangling /etc/resolv.conf stub symlink breaks name resolution for the whole
# box and makes resolvectl hang on D-Bus activation.
if [[ -e /usr/lib/systemd/system/systemd-resolved.service ]] && ! systemctl is-active --quiet systemd-resolved; then
  echo "warning: systemd-resolved is installed but not running" >&2
  echo "  /etc/resolv.conf may point at its unavailable stub — DNS will fail" >&2
  echo "  fix with: systemctl enable --now systemd-resolved" >&2
fi

echo "Install complete. Run: combiner-status"
if [[ "$DHCP_ENABLED" == "1" ]]; then
  echo "Status: http://<control-ip>:8080/  (clients on Control; use combiner Control IP)"
else
  echo "Status: http://<control-ip-or-mgmt-ip>:8080/"
fi
