#!/usr/bin/env bash
# Install combiner lab profile on Raspberry Pi OS / Debian.
# Fail-closed: load drop-forward rules BEFORE enabling IP forwarding.
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: install.sh [SITE_YAML] [--i-have-console] [--offline-debs DIR]

  SITE_YAML           path to site.yaml (default /etc/combiner/site.yaml)
  --i-have-console    proceed over SSH, accepting that this may lock you out
  --offline-debs DIR  install runtime packages from .deb files in DIR instead
                      of apt (for racked units with no Internet)
USAGE
}

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SITE_YAML=""
I_HAVE_CONSOLE=0
OFFLINE_DEBS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --i-have-console) I_HAVE_CONSOLE=1; shift ;;
    --offline-debs)
      [[ -n "${2:-}" ]] || { echo "--offline-debs needs a directory" >&2; exit 1; }
      OFFLINE_DEBS="$2"; shift 2 ;;
    --offline-debs=*) OFFLINE_DEBS="${1#--offline-debs=}"; shift ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
    *)
      if [[ -n "$SITE_YAML" ]]; then
        echo "unexpected argument: $1" >&2; usage >&2; exit 1
      fi
      SITE_YAML="$1"; shift ;;
  esac
done
SITE_YAML="${SITE_YAML:-/etc/combiner/site.yaml}"

if [[ -n "$OFFLINE_DEBS" && ! -d "$OFFLINE_DEBS" ]]; then
  echo "--offline-debs: not a directory: $OFFLINE_DEBS" >&2
  exit 1
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

# Preflight against a STAGED copy of what will be installed, so that nothing on
# this box changes until the config is known good. allowlist_files resolve
# relative to site.yaml, so the staged pair has to mirror the final /etc layout
# exactly -- which it does, because the real install below copies this tree.
STAGED_ETC="$STAGING/etc-combiner"
mkdir -p "$STAGED_ETC/allowlists"
cp "$SITE_YAML" "$STAGED_ETC/site.yaml"
cp -a "$ROOT/config/allowlists/." "$STAGED_ETC/allowlists/"

# Authoritative preflight: the Go loader is the schema (it rejects unknown keys
# and cross-checks allowlists against the deny floor).
if ! "$BIN_DIR/combiner" -check -config "$STAGED_ETC/site.yaml"; then
  echo "site.yaml failed validation — aborting before any change" >&2
  if [[ "$STALE_BINARY" -eq 1 ]]; then
    echo "note: the binary above predates this checkout — rebuild it (make build)" >&2
    echo "      or use a release tarball before trusting this rejection" >&2
  fi
  exit 1
fi

# Read site.yaml facts through the Go loader rather than python3-yaml: this
# script is responsible for INSTALLING python3-yaml, so it cannot depend on it
# to decide what to install.
FACTS="$("$BIN_DIR/combiner" -config "$STAGED_ETC/site.yaml" -print-facts)" || {
  echo "could not read site.yaml facts — aborting before any change" >&2
  exit 1
}
eval "$FACTS"
DHCP_ENABLED="$COMBINER_MGMT_DHCP_ENABLED"

# --- runtime dependencies --------------------------------------------------
# A racked combiner has no Internet, so never reach for apt on a box that
# already has what it needs, and fail with an actionable list rather than deep
# inside apt on one that cannot reach a mirror.
REQUIRED_PKGS=()
missing_pkgs() {
  REQUIRED_PKGS=()
  command -v nft       >/dev/null 2>&1 || REQUIRED_PKGS+=(nftables)
  command -v ip        >/dev/null 2>&1 || REQUIRED_PKGS+=(iproute2)
  command -v conntrack >/dev/null 2>&1 || REQUIRED_PKGS+=(conntrack)
  command -v python3   >/dev/null 2>&1 || REQUIRED_PKGS+=(python3)
  # The nftables/networkd generators import yaml; the Go binary does not.
  python3 -c 'import yaml' >/dev/null 2>&1 || REQUIRED_PKGS+=(python3-yaml)
  if [[ "$DHCP_ENABLED" == "1" ]]; then
    command -v dnsmasq >/dev/null 2>&1 || REQUIRED_PKGS+=(dnsmasq)
  fi
}

apt_install() {
  export DEBIAN_FRONTEND=noninteractive
  # No retries and a hard timeout: with no uplink this must fail fast instead
  # of sitting in DNS and connect timeouts for minutes.
  timeout 180 apt-get update -y -o Acquire::Retries=0 ||
    echo "warning: apt-get update failed — trying the local package cache" >&2
  timeout 300 apt-get install -y -o Acquire::Retries=0 "$@"
}

missing_pkgs
if [[ ${#REQUIRED_PKGS[@]} -eq 0 ]]; then
  echo "all runtime dependencies present — skipping apt"
elif [[ -n "$OFFLINE_DEBS" ]]; then
  echo "installing from $OFFLINE_DEBS: ${REQUIRED_PKGS[*]}"
  shopt -s nullglob
  DEBS=("$OFFLINE_DEBS"/*.deb)
  shopt -u nullglob
  if [[ ${#DEBS[@]} -eq 0 ]]; then
    echo "no .deb files in $OFFLINE_DEBS" >&2
    echo "nothing has been changed on this system" >&2
    exit 1
  fi
  # dpkg exits non-zero on unmet dependencies but may still have unpacked
  # everything that matters, so let the re-check below be the verdict.
  if ! dpkg -i "${DEBS[@]}"; then
    echo "warning: dpkg reported errors — verifying what is usable below" >&2
  fi
elif ! apt_install "${REQUIRED_PKGS[@]}"; then
  echo "" >&2
  echo "cannot install missing runtime dependencies: ${REQUIRED_PKGS[*]}" >&2
  echo "a racked combiner usually has no Internet. Either:" >&2
  echo "  - pre-install them where apt works:" >&2
  echo "      sudo apt-get install -y ${REQUIRED_PKGS[*]}" >&2
  echo "  - or stage the .deb files on removable media and re-run with:" >&2
  echo "      sudo $0 $SITE_YAML --offline-debs /path/to/debs" >&2
  echo "nothing has been changed on this system" >&2
  exit 1
fi

# Installing is not the same as working: a partial dpkg run exits 0 per-package
# but can still leave a dependency unmet.
missing_pkgs
if [[ ${#REQUIRED_PKGS[@]} -ne 0 ]]; then
  echo "still missing after install: ${REQUIRED_PKGS[*]}" >&2
  exit 1
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

# networkd only hands DNS= to systemd-resolved, so install it just for the lab
# uplink case. Installing it rewrites /etc/resolv.conf as a symlink to its stub,
# which leaves the box with no DNS at all if resolved is not actually running.
MGMT_DNS_COUNT="$COMBINER_MGMT_DNS_COUNT"
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

# Install the runtime pieces. The generators go to a stable path so
# combiner-apply works at boot without the unpacked release tree still being
# around.
install -m 0755 "$BIN_DIR/combiner" /usr/local/bin/combiner
install -m 0755 "$BIN_DIR/combiner-status" /usr/local/bin/combiner-status
install -m 0755 "$ROOT/deploy/pi/combiner-apply.sh" /usr/local/sbin/combiner-apply
install -d /usr/local/lib/combiner
install -m 0644 "$ROOT/deploy/pi/generate-nftables.py" \
                "$ROOT/deploy/pi/generate-network-config.py" \
                "$ROOT/deploy/pi/site_config.py" \
                /usr/local/lib/combiner/
install -m 0644 "$ROOT/deploy/pi/systemd/combiner.service" /etc/systemd/system/combiner.service
install -m 0644 "$ROOT/deploy/pi/systemd/combiner-apply.service" /etc/systemd/system/combiner-apply.service

systemctl daemon-reload
systemctl enable nftables combiner combiner-apply

# Everything config-derived now lives in combiner-apply, so a first install and
# a later re-home run exactly the same code path. --force because an install
# must apply even when the config happens to match what is already live.
/usr/local/sbin/combiner-apply \
  --config "$SITE_YAML" \
  --allowlists "$ROOT/config/allowlists" \
  --force

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
