#!/usr/bin/env bash
# Apply a site config to this box: nftables, systemd-networkd, hostname,
# optional Mgmt DHCP, and IP forwarding.
#
# Needs no network. Safe to run on every boot, and does nothing at all when the
# config already matches what is live — so a normal boot restarts nothing.
#
# This is what makes a racked, headless unit re-homeable: drop a different
# combiner-site.yaml on the FAT boot partition, power cycle, and the box paves
# the new config over the old one. install.sh does the one-time OS preparation
# and then calls this for the config half.
#
# Fail-closed, in the same order install.sh always used: validate first, drop
# all forwarding, load the ruleset, only then re-enable forwarding.
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"
BOOT_CONFIG="$BOOT_DIR/combiner-site.yaml"
HOLD_MARKER="$BOOT_DIR/combiner-provisioning"
ETC_CONFIG="/etc/combiner/site.yaml"
LIBDIR="/usr/local/lib/combiner"
SELF_DIR="$(cd "$(dirname "$0")" && pwd)"

CONFIG=""
ALLOWLISTS=""
FORCE=0
DRY_RUN=0

usage() {
  cat <<'USAGE'
usage: combiner-apply [options]

  --config FILE       site config to apply. Default: the boot partition's
                      combiner-site.yaml when present, else /etc/combiner/site.yaml
  --allowlists DIR    allowlists to install alongside it (default: keep the
                      ones already in /etc/combiner/allowlists)
  --force             apply even when nothing changed
  --dry-run           report what would change; change nothing
  -h, --help          this text
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)     CONFIG="${2:?--config needs a file}"; shift 2 ;;
    --allowlists) ALLOWLISTS="${2:?--allowlists needs a directory}"; shift 2 ;;
    --force)      FORCE=1; shift ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "combiner-apply: $*" >&2; exit 1; }
say() { echo "combiner-apply: $*"; }

[[ "$(id -u)" -eq 0 ]] || die "run as root"

# A headless unit that refuses a config has to be diagnosable from the card, so
# mirror everything onto the boot partition when one is writable.
LOGDIR="$(dirname "$BOOT_CONFIG")"
if [[ -d "$LOGDIR" && -w "$LOGDIR" ]]; then
  exec > >(tee -a "$LOGDIR/combiner-apply.log") 2>&1
fi

# Provisioning hold. First boot runs on whatever DHCP the bench hands out and
# applies nothing, so the unit stays reachable while it is verified; the config
# lands when combiner-go-live removes this marker and reboots. Checked here
# rather than in the unit file so the /boot fallback above is honoured, and so
# the reason is visible in combiner-apply.log.
#
# --force is the deliberate override: install.sh and an operator debugging by
# hand both mean it when they pass it. --dry-run is exempt because it changes
# nothing, and because it is how combiner-go-live validates the config it is
# about to commit to — while the hold is by definition still in place.
if [[ -e "$HOLD_MARKER" && "$FORCE" -eq 0 && "$DRY_RUN" -eq 0 ]]; then
  say "provisioning hold in place ($HOLD_MARKER) — not applying any config"
  say "this unit is waiting for: sudo combiner-go-live"
  exit 0
fi

# The boot partition wins: it is the copy a person can edit with the card in a
# laptop, so it is the source of truth for a box with no console.
if [[ -z "$CONFIG" ]]; then
  if [[ -f "$BOOT_CONFIG" ]]; then
    CONFIG="$BOOT_CONFIG"
  elif [[ -f "$ETC_CONFIG" ]]; then
    CONFIG="$ETC_CONFIG"
  else
    die "no site config found (looked in $BOOT_CONFIG and $ETC_CONFIG)"
  fi
fi
[[ -f "$CONFIG" ]] || die "no such config: $CONFIG"

# Installed generators first; fall back to a release tree for install.sh's call.
GEN_DIR="$LIBDIR"
[[ -f "$GEN_DIR/generate-nftables.py" ]] || GEN_DIR="$SELF_DIR"
for g in generate-nftables.py generate-network-config.py; do
  [[ -f "$GEN_DIR/$g" ]] || die "missing generator $g (looked in $LIBDIR and $SELF_DIR)"
done

command -v combiner >/dev/null 2>&1 || die "combiner binary not installed — run install.sh first"

STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT

# --- stage exactly what would be installed ----------------------------------
STAGED_ETC="$STAGING/etc-combiner"
mkdir -p "$STAGED_ETC/allowlists"
cp "$CONFIG" "$STAGED_ETC/site.yaml"
if [[ -n "$ALLOWLISTS" ]]; then
  [[ -d "$ALLOWLISTS" ]] || die "no such allowlists directory: $ALLOWLISTS"
  cp -a "$ALLOWLISTS/." "$STAGED_ETC/allowlists/"
elif [[ -d /etc/combiner/allowlists ]]; then
  cp -a /etc/combiner/allowlists/. "$STAGED_ETC/allowlists/"
else
  die "no allowlists to install (pass --allowlists DIR)"
fi

# --- validate BEFORE anything is touched ------------------------------------
# The Go loader is the schema. A rejected config must leave the running box
# exactly as it was, because that box may be the only thing still working.
if ! combiner -check -config "$STAGED_ETC/site.yaml"; then
  die "config rejected — nothing has been changed
The unit is still running its previous configuration. Fix the config on the
boot partition and power cycle, or run combiner-apply again by hand."
fi

eval "$(combiner -config "$STAGED_ETC/site.yaml" -print-facts)"

NFT_STAGE="$STAGING/nftables.conf"
python3 "$GEN_DIR/generate-nftables.py" "$STAGED_ETC/site.yaml" "$NFT_STAGE" >/dev/null
python3 "$GEN_DIR/generate-network-config.py" "$STAGED_ETC/site.yaml" "$STAGING" >/dev/null

# generate-nftables.py stamps its INPUT path into a header comment. Ours is a
# temp staging copy, so without this every boot would see a "difference" that is
# only the temp directory's name — and the installed file would claim it came
# from /tmp. Rewrite it to the path it will have been generated from.
sed -i "s|^# Generated by generate-nftables.py from .*|# Generated by generate-nftables.py from $ETC_CONFIG|" "$NFT_STAGE"

if ! nft -c -f "$NFT_STAGE"; then
  die "generated ruleset failed nft -c — nothing has been changed"
fi

# --- has anything actually changed? -----------------------------------------
# Derived from the live system rather than a stored hash, so there is no state
# to get out of sync and a hand-edited /etc is noticed too.
CHANGES=()
cmp -s "$STAGED_ETC/site.yaml" "$ETC_CONFIG" 2>/dev/null || CHANGES+=("site.yaml")
cmp -s "$NFT_STAGE" /etc/nftables.conf 2>/dev/null || CHANGES+=("nftables ruleset")
diff -r -q "$STAGED_ETC/allowlists" /etc/combiner/allowlists >/dev/null 2>&1 || CHANGES+=("allowlists")

LIVE_NET="$STAGING/live-network"
mkdir -p "$LIVE_NET"
for f in /etc/systemd/network/*combiner*.netdev /etc/systemd/network/*combiner*.network; do
  [[ -e "$f" ]] && cp "$f" "$LIVE_NET/"
done
diff -r -q "$STAGING/systemd/network" "$LIVE_NET" >/dev/null 2>&1 || CHANGES+=("networkd units")

[[ "$(cat /etc/hostname 2>/dev/null || true)" == "$COMBINER_HOSTNAME" ]] || CHANGES+=("hostname")

if [[ ${#CHANGES[@]} -eq 0 && "$FORCE" -eq 0 ]]; then
  say "no change: $CONFIG already applied"
  exit 0
fi

if [[ ${#CHANGES[@]} -eq 0 ]]; then
  say "no change, but --force given — reapplying"
else
  say "applying $CONFIG (changed: ${CHANGES[*]})"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  say "--dry-run: nothing was changed"
  exit 0
fi

# --- apply ------------------------------------------------------------------
# Forwarding OFF first: between here and the new ruleset going live this box
# must not bridge anything.
write_forwarding() {
  mkdir -p /etc/sysctl.d
  cat >/etc/sysctl.d/99-combiner.conf <<EOF
net.ipv4.ip_forward=$1
net.ipv4.conf.all.forwarding=$1
net.ipv4.conf.default.forwarding=$1
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
EOF
  sysctl --system >/dev/null
}
write_forwarding 0

# Fail-closed bootstrap: drop all forwarding while the ruleset is swapped.
nft flush ruleset
nft -f - <<'EOF'
table inet combiner_bootstrap {
  chain forward {
    type filter hook forward priority filter; policy drop;
  }
}
EOF

if [[ -f /etc/nftables.conf ]]; then
  cp -a /etc/nftables.conf "/etc/nftables.conf.bak.$(date +%s)" || true
fi
cp "$NFT_STAGE" /etc/nftables.conf
nft -f /etc/nftables.conf
# Stale conntrack entries would let flows established under the OLD ruleset
# keep bypassing the new one.
conntrack -F 2>/dev/null ||
  say "warning: conntrack not installed — established flows keep their old path until they expire"

mkdir -p /etc/combiner/allowlists
cp "$STAGED_ETC/site.yaml" "$ETC_CONFIG"
cp -a "$STAGED_ETC/allowlists/." /etc/combiner/allowlists/

mkdir -p /etc/systemd/network
# A leftover .netdev whose Name matches a renamed or now-untagged interface
# keeps networkd from managing that link.
rm -f /etc/systemd/network/*combiner*.netdev /etc/systemd/network/*combiner*.network
cp -a "$STAGING/systemd/network/." /etc/systemd/network/

echo "$COMBINER_HOSTNAME" >/etc/hostname
hostnamectl set-hostname "$COMBINER_HOSTNAME" 2>/dev/null || hostname "$COMBINER_HOSTNAME"

systemctl restart systemd-networkd
networkctl reload 2>/dev/null || true
systemctl restart nftables

# networkd starting is not proof it configured anything.
MISSING=""
for _ in $(seq 1 15); do
  MISSING=""
  while read -r role ifname; do
    [[ -z "$ifname" ]] && continue
    [[ -e "/sys/class/net/$ifname" ]] || MISSING+="$role=$ifname "
  done <"$STAGING/combiner-interfaces.txt"
  [[ -z "$MISSING" ]] && break
  sleep 2
done

if [[ -n "$MISSING" ]]; then
  echo "combiner-apply: systemd-networkd did not create: $MISSING" >&2
  echo "  forwarding is still OFF; the box will not bridge Control and Dante" >&2
  echo "  if Mgmt should be native/untagged on $COMBINER_PHYSICAL_INTERFACE," >&2
  echo "  set vlans.mgmt.untagged: true in the site config" >&2
  networkctl list --no-pager >&2 2>&1 || true
  ip -br addr >&2 || true
  exit 1
fi

# Mgmt DHCP only when the config asks for it.
if [[ "$(cat "$STAGING/combiner-mgmt-dhcp.enabled")" == "1" ]]; then
  mkdir -p /etc/dnsmasq.d
  if [[ -f /etc/dnsmasq.conf ]]; then
    sed -i 's/^#\?bind-interfaces.*/bind-interfaces/' /etc/dnsmasq.conf || true
  fi
  cp "$STAGING/dnsmasq.d/combiner-mgmt.conf" /etc/dnsmasq.d/combiner-mgmt.conf
  systemctl enable dnsmasq >/dev/null 2>&1 || true
  systemctl restart dnsmasq
else
  rm -f /etc/dnsmasq.d/combiner-mgmt.conf
  systemctl disable --now dnsmasq 2>/dev/null || true
fi

# Forwarding ONLY after the rules are live.
write_forwarding 1

# --no-block: this may run as a boot unit ordered BEFORE combiner.service, and
# a blocking restart of a unit that is ordered after us deadlocks until timeout.
systemctl restart --no-block combiner 2>/dev/null || true

say "applied $CONFIG"
