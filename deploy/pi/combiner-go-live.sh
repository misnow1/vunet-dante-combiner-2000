#!/usr/bin/env bash
# Take a provisioned unit off the bench network and onto its production config.
#
# Provisioning needs a mirror, DHCP and DNS; a rack has none of those. So the
# first boot runs on whatever DHCP the bench hands out and applies NOTHING —
# it holds, and stays reachable, so the unit can be verified while it is still
# on a network you can reach it from. This script is the commit.
#
# It does the two things that first boot deliberately left undone: hand the
# interfaces from NetworkManager to systemd-networkd, and enable the units that
# apply the site config. Then it clears the hold and reboots, and
# combiner-apply paves the card's config in on the way up.
#
# Needs no network. The hold marker lives on the FAT boot partition, so a unit
# that will not boot can still be released by hand from a laptop.
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"
HOLD_MARKER="$BOOT_DIR/combiner-provisioning"
BOOT_CONFIG="$BOOT_DIR/combiner-site.yaml"
LOG="$BOOT_DIR/combiner-golive.log"

ASSUME_YES=0
ACTION="live"
DO_REBOOT=1
# Long enough to read the closing message and reach for the power, short enough
# that nobody sits watching it.
REBOOT_DELAY=10

usage() {
  cat <<'USAGE'
usage: combiner-go-live [--yes] [--no-reboot]
       combiner-go-live --undo [--yes] [--no-reboot]
       combiner-go-live --status

  (default)    apply this unit's production config and reboot into it
  --undo       put a live unit back into the hold: hand the interfaces back to
               NetworkManager, disable the runtime units, and reboot onto DHCP
  --yes        proceed without the confirmation prompt
  --no-reboot  do everything except the reboot (the change applies on the next
               boot either way; useful when you want to pull the power instead)
  --status     report whether this unit is held or live, and exit. Reads
               nothing privileged, but /usr/local/sbin is not on a normal
               user's PATH, so it still wants sudo or the full path
  -h, --help   this text

The unit is UNREACHABLE from the bench network afterwards: it moves to the
addressing in its combiner-site.yaml. Verify before you run this, not after.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|-y)    ASSUME_YES=1; shift ;;
    --no-reboot) DO_REBOOT=0; shift ;;
    --undo)      ACTION="undo"; shift ;;
    --status)    ACTION="status"; shift ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "combiner-go-live: $*" >&2; exit 1; }
say() { echo "combiner-go-live: $*"; }

held() { [[ -e "$HOLD_MARKER" ]]; }

# allowlist_files in site.yaml resolve relative to the config's OWN directory,
# and the card has no allowlists/ next to combiner-site.yaml — only /etc does.
# Checking the card copy in place therefore fails on a perfectly good config,
# which is exactly what this reported until a lab Pi said "config valid: NO"
# about one combiner-apply then applied without complaint. Stage a copy in the
# layout the unit actually uses, the same way prep-card.sh and combiner-apply
# both do, and check that.
STAGED=""
stage_config() {
  [[ -r /etc/combiner/allowlists ]] || return 1
  local d
  d="$(mktemp -d 2>/dev/null)" || return 1
  cp "$BOOT_CONFIG" "$d/site.yaml" 2>/dev/null || { rm -rf "$d"; return 1; }
  cp -a /etc/combiner/allowlists "$d/" 2>/dev/null || { rm -rf "$d"; return 1; }
  STAGED="$d/site.yaml"
  STAGE_DIR="$d"
  return 0
}
STAGE_DIR=""
cleanup_stage() { [[ -n "$STAGE_DIR" ]] && rm -rf "$STAGE_DIR"; }
trap cleanup_stage EXIT

# --- status -----------------------------------------------------------------
# Reads nothing privileged, like combiner-lock --status: "which state is this
# box in" is the first question anyone asks. In practice it is still typed with
# sudo, because /usr/local/sbin is absent from a normal user's PATH — worth
# knowing rather than papering over, since combiner-status (in /usr/local/bin,
# which IS on PATH) reports the hold too and needs no privilege at all.
if [[ "$ACTION" == "status" ]]; then
  echo "  state:           $(held && echo 'HELD — awaiting go-live' || echo 'live')"
  echo "  card config:     $([[ -f "$BOOT_CONFIG" ]] && echo "$BOOT_CONFIG" || echo 'MISSING')"
  if [[ -f "$BOOT_CONFIG" ]] && command -v combiner >/dev/null 2>&1; then
    if stage_config; then
      echo "  config valid:    $(combiner -check -config "$STAGED" >/dev/null 2>&1 && echo yes || echo 'NO — combiner -check rejects it')"
      echo "  becomes host:    $(combiner -config "$STAGED" -print-facts 2>/dev/null | sed -n "s/^COMBINER_HOSTNAME=//p" | tr -d "'")"
    else
      echo "  config valid:    unknown — cannot read /etc/combiner/allowlists"
    fi
  fi
  echo "  networkd:        $(systemctl is-enabled systemd-networkd 2>&1)"
  echo "  NetworkManager:  $(systemctl is-enabled NetworkManager 2>&1)"
  echo "  combiner-apply:  $(systemctl is-enabled combiner-apply.service 2>&1)"
  echo "  combiner:        $(systemctl is-enabled combiner.service 2>&1)"
  exit 0
fi

[[ "$(id -u)" -eq 0 ]] || die "run as root"

# Every step in either direction is a systemctl enable/disable, which writes
# symlinks under /etc/systemd. Under an overlay those land in tmpfs and evaporate
# on the very reboot this script performs, so the unit would come back unchanged,
# having reported success.
if grep -q 'overlayroot=tmpfs' /proc/cmdline 2>/dev/null; then
  die "this unit's root is read-only, so nothing done here would survive the reboot.
Release it first:

    combiner-lock --off
    sudo reboot"
fi

# --- undo -------------------------------------------------------------------
# Re-creating the marker on a live unit is NOT enough on its own, and assuming
# it is was worth this whole branch: the hold stops combiner-apply from applying
# a config, but it does not unpick one already applied. The networkd units, the
# ruleset and the hostname are all still in /etc, and NetworkManager is still
# disabled — so the unit would come back on show addressing, held and just as
# unreachable. Going back to a bench means undoing the handover.
if [[ "$ACTION" == "undo" ]]; then
  ! held || die "this unit is already holding — nothing to undo."

  # Recorded before the log redirect below: once stdout is a pipe into tee,
  # `[[ -t 1 ]]` is false no matter who is at the console.
  INTERACTIVE=0
  [[ -t 1 ]] && INTERACTIVE=1

  systemctl list-unit-files NetworkManager.service >/dev/null 2>&1 ||
    die "NetworkManager is not installed, so there is nothing to hand the
interfaces back to. Undoing would leave this unit with no network at all."

  if [[ "$ASSUME_YES" -eq 0 ]]; then
    [[ -t 0 ]] || die "not a terminal — pass --yes to proceed non-interactively"
    echo ""
    echo "This puts the unit back on DHCP: the combiner networkd units are"
    echo "removed, NetworkManager takes the interfaces back, and nftables,"
    echo "combiner and combiner-apply are disabled. Its config stays on the"
    echo "card, and combiner-go-live applies it again."
    echo ""
    echo "If this unit is in a rack, you are about to take it off the show."
    read -rp "Put it back into the hold? [y/N] " reply
    [[ "$reply" == "y" || "$reply" == "Y" ]] || die "aborted — nothing was changed"
  fi

  if [[ -d "$BOOT_DIR" && -w "$BOOT_DIR" ]]; then
    exec > >(tee -a "$LOG") 2>&1
  fi
  say "undo: started $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

  # First, so a failure part-way through still leaves a unit that holds rather
  # than one that re-applies the config it is being taken off.
  : >"$HOLD_MARKER"

  systemctl disable nftables combiner combiner-apply >/dev/null 2>&1 || true
  # A leftover .netdev keeps networkd from ever managing the plain link again,
  # which is the same trap combiner-apply clears on every re-home.
  rm -f /etc/systemd/network/*combiner*.netdev /etc/systemd/network/*combiner*.network
  systemctl disable systemd-networkd >/dev/null 2>&1 || true
  systemctl enable NetworkManager >/dev/null 2>&1 || true
  # Symmetry with the handover: no reflector runs on a held unit, so nothing
  # conflicts with avahi, and mDNS is how anyone finds this box again.
  systemctl unmask avahi-daemon >/dev/null 2>&1 || true
  systemctl enable avahi-daemon >/dev/null 2>&1 || true
  # Forwarding off: a unit on a bench must not bridge anything. combiner-apply
  # writes this file again when the unit next goes live.
  rm -f /etc/sysctl.d/99-combiner.conf
  sync

  /usr/local/sbin/combiner-led auto 2>/dev/null || true

  say "undone. rebooting onto DHCP; run combiner-go-live when you want it back"
  if [[ "$DO_REBOOT" -eq 1 ]]; then
    say "rebooting in ${REBOOT_DELAY}s. Ctrl-C stops the reboot, not the undo:"
    say "the hold is already back and the units already disabled."
    if [[ "$INTERACTIVE" -eq 1 ]]; then
      for ((i = REBOOT_DELAY; i > 0; i--)); do
        printf '\r  rebooting in %2ds ' "$i" >/dev/tty 2>/dev/null || true
        sleep 1
      done
      printf '\r%*s\r' 24 '' >/dev/tty 2>/dev/null || true
    fi
    systemctl reboot
  fi
  exit 0
fi

# --- guards -----------------------------------------------------------------
held || die "this unit is not holding — it has already gone live.
Nothing to do. To re-apply its config by hand:

    sudo combiner-apply --force

To take it back off its production network and onto DHCP:

    sudo combiner-go-live --undo"

[[ -x /usr/local/sbin/combiner-apply ]] ||
  die "combiner-apply is not installed — provisioning did not finish.
Read $BOOT_DIR/combiner-firstboot.log."

[[ -f "$BOOT_CONFIG" ]] ||
  die "no $BOOT_CONFIG — this unit has no config to go live with.
Put the card in a laptop and stage one (deploy/pi/prep-card.sh --site ...)."

# --- preflight --------------------------------------------------------------
# The whole reason for holding is that a rejected config must be found while
# the unit is still reachable. --dry-run runs the real generators and the real
# nft -c, and changes nothing.
say "validating $BOOT_CONFIG before committing to it"
if ! /usr/local/sbin/combiner-apply --config "$BOOT_CONFIG" --dry-run; then
  die "this config would not apply — the unit is unchanged and still on the bench network.
Fix combiner-site.yaml (on the card, or in $BOOT_DIR) and re-run."
fi

# --- confirm ----------------------------------------------------------------
# Before the log redirect: read -p writes its prompt to stderr, and teeing that
# into a file on a FAT partition is a good way to have a prompt appear after
# the read has already blocked.
if [[ "$ASSUME_YES" -eq 0 ]]; then
  [[ -t 0 ]] || die "not a terminal — pass --yes to proceed non-interactively"
  NEW_HOST=""
  stage_config && NEW_HOST="$(combiner -config "$STAGED" -print-facts 2>/dev/null | sed -n "s/^COMBINER_HOSTNAME=//p" | tr -d "'")"
  echo ""
  echo "This unit is about to leave the bench network for good."
  echo ""
  echo "  config:    $BOOT_CONFIG"
  echo "  hostname:  ${NEW_HOST:-unknown}"
  echo ""
  echo "After the reboot it answers only on the addressing in that config, so"
  echo "this SSH session is the last one you get on the current network."
  read -rp "Go live? [y/N] " reply
  [[ "$reply" == "y" || "$reply" == "Y" ]] || die "aborted — nothing was changed"
fi

# --- commit -----------------------------------------------------------------
# Whether a person is watching, recorded BEFORE the redirect below: once stdout
# is a pipe into tee, `[[ -t 1 ]]` is false no matter who is at the console.
INTERACTIVE=0
[[ -t 1 ]] && INTERACTIVE=1

# From here on, mirror everything onto the boot partition: this is the step
# after which the unit stops being reachable, so it is the step whose record
# has to survive on a card someone can read in a laptop.
if [[ -d "$BOOT_DIR" && -w "$BOOT_DIR" ]]; then
  exec > >(tee -a "$LOG") 2>&1
fi

say "started $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
say "config $BOOT_CONFIG passed combiner-apply --dry-run"

# systemd-networkd is enabled but not started: the swap happens at the reboot,
# in one step, rather than half-applied on a live box.
say "handing the interfaces to systemd-networkd"
systemctl enable systemd-networkd >/dev/null

# NOT --now. Tearing NetworkManager down here would kill the SSH session this
# is almost certainly running over, before the closing message is printed and
# before the marker is cleared — leaving a unit that is both unreachable and
# still held. The reboot is what makes the swap take effect.
for svc in NetworkManager dhcpcd; do
  systemctl disable "$svc" >/dev/null 2>&1 || true
done

# Now, not at provisioning time: avahi only conflicts with the combiner mDNS
# reflector on udp/5353 once that reflector exists, and until this moment it
# did not. Leaving avahi up through the hold is what lets combiner.local find
# a unit whose DHCP address nobody chose.
say "masking avahi (it would fight the mDNS reflector on udp/5353)"
systemctl disable --now avahi-daemon >/dev/null 2>&1 || true
systemctl mask avahi-daemon >/dev/null 2>&1 || true

say "enabling the runtime units"
systemctl enable nftables combiner combiner-apply >/dev/null

# Last, and only once everything above succeeded: while this file exists,
# combiner-apply refuses to touch the network.
rm -f "$HOLD_MARKER"
sync

# auto, not "running": with --no-reboot (how combiner-seal calls this) the unit
# is not actually running anything yet, and claiming otherwise on the LED is the
# one thing it must not do.
/usr/local/sbin/combiner-led auto 2>/dev/null || true

cat <<EOF

Going live. On the next boot this unit applies $BOOT_CONFIG and answers on its
production addressing — not on this network.

If it does not come back, pull the card and read combiner-apply.log on it.
To bring it back to a bench network later:

    sudo combiner-go-live --undo
EOF

if [[ "$DO_REBOOT" -eq 1 ]]; then
  # Nothing below can be undone by interrupting it. The hold is already cleared
  # and the units are already enabled, so this unit is live on its next boot
  # whether that boot happens in ten seconds or next week. The pause is to let
  # a person read what just scrolled past and pull the plug on their own terms,
  # not an offer to cancel — saying otherwise would be a lie at the one moment
  # the operator most needs to trust this output.
  say "rebooting in ${REBOOT_DELAY}s. Ctrl-C stops the reboot, not the go-live:"
  say "this unit is committed either way, and applies its config whenever it"
  say "next boots. To put it back on a bench: combiner-go-live --undo"
  if [[ "$INTERACTIVE" -eq 1 ]]; then
    # Animate on the tty rather than through the tee, or the log fills with
    # carriage returns and half-drawn counters.
    for ((i = REBOOT_DELAY; i > 0; i--)); do
      printf '\r  rebooting in %2ds ' "$i" >/dev/tty 2>/dev/null || true
      sleep 1
    done
    printf '\r%*s\r' 24 '' >/dev/tty 2>/dev/null || true
  fi
  say "rebooting"
  systemctl reboot
else
  say "--no-reboot: the config applies on the next boot"
fi
