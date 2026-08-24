#!/usr/bin/env bash
# Strip per-unit identity from a provisioned card so it can be imaged and
# written to spares.
#
# Provisioning needs apt and a rack has no mirror, so spares cannot be made by
# flashing a blank card in the field. The workable path is: provision one unit
# on a bench, seal it, image the card, write N copies, and drop each rig's
# combiner-site.yaml on each copy's boot partition. combiner-apply paves the
# per-rig config in on first boot.
#
# What this KEEPS is the point: packages, binaries, units, and cloud-init's
# record that provisioning already happened. A clone must not try to
# re-provision — that would need the Internet it does not have.
#
# What it CLEARS is everything that must not be shared by a fleet: machine-id
# (systemd-networkd derives its DHCP DUID from it, so clones would collide on
# leases), SSH host keys (otherwise one compromised card is every unit), the
# random seed, logs, and the golden unit's site config.
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"

ASSUME_YES=0
DRY_RUN=0
POWEROFF=0

usage() {
  cat <<'USAGE'
usage: combiner-seal [--yes] [--dry-run] [--poweroff]

  --yes        proceed without the confirmation prompt
  --dry-run    list what would be cleared; change nothing
  --poweroff   power off when done, so the card can be pulled and imaged
  -h, --help   this text

Run this as the LAST thing before imaging a card. It leaves the unit without an
identity: it is meant to be powered off and cloned, not kept running.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes|-y)   ASSUME_YES=1; shift ;;
    --dry-run)  DRY_RUN=1; shift ;;
    --poweroff) POWEROFF=1; shift ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "combiner-seal: $*" >&2; exit 1; }
say() { echo "combiner-seal: $*"; }

[[ "$(id -u)" -eq 0 ]] || die "run as root"

if [[ "$DRY_RUN" -eq 0 && "$ASSUME_YES" -eq 0 ]]; then
  [[ -t 0 ]] || die "not a terminal — pass --yes to proceed non-interactively"
  echo "This clears this unit's identity: machine-id, SSH host keys, random"
  echo "seed, logs, and /etc/combiner/site.yaml. SSH host keys change, so you"
  echo "will get a host-key warning if you ever boot this card again."
  read -rp "Seal this card for cloning? [y/N] " reply
  [[ "$reply" == "y" || "$reply" == "Y" ]] || die "aborted"
fi

# do <description> <command...>
do_step() {
  local desc="$1"; shift
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "  would: $desc"
    return 0
  fi
  echo "  $desc"
  "$@"
}

say "sealing (boot partition: $BOOT_DIR)"

# --- identity ---------------------------------------------------------------
# Truncate rather than delete: an EMPTY /etc/machine-id is systemd's documented
# "not yet initialised" state, so systemd generates a fresh one on next boot.
# /var/lib/dbus/machine-id is a symlink to this file, so it is covered too.
#
# Note this does NOT reliably make ConditionFirstBoot=yes fire on Raspberry Pi
# OS — a sealed card came up with the condition unmet and therefore no host
# keys. Hence combiner-hostkeys.service below, which does not depend on it.
do_step "truncate /etc/machine-id (also covers dbus, a symlink to it)" \
  truncate -s 0 /etc/machine-id

# A clone must never present the golden unit's host identity.
do_step "remove SSH host keys (combiner-hostkeys.service regenerates them)" \
  bash -c 'rm -f /etc/ssh/ssh_host_*'

# A shared seed means every clone starts from the same entropy.
do_step "remove systemd random seed" rm -f /var/lib/systemd/random-seed

# --- per-unit state ---------------------------------------------------------
# Every clone must carry its own combiner-site.yaml on its boot partition.
# Leaving the golden unit's config in /etc would let a clone whose card is
# missing one silently come up on the WRONG addressing; without it,
# combiner-apply fails loudly instead.
do_step "remove /etc/combiner/site.yaml (each clone must bring its own)" \
  rm -f /etc/combiner/site.yaml

do_step "reset hostname to 'combiner' (site.yaml sets the real one)" \
  bash -c 'echo combiner >/etc/hostname'

do_step "clear DHCP leases" \
  bash -c 'rm -rf /var/lib/dhcp/* /var/lib/NetworkManager/*.lease* 2>/dev/null || true'

# --- logs -------------------------------------------------------------------
do_step "clear the journal" \
  bash -c 'journalctl --rotate >/dev/null 2>&1 || true
           journalctl --vacuum-time=1s >/dev/null 2>&1 || true
           rm -rf /var/log/journal/* 2>/dev/null || true'

do_step "truncate /var/log files" \
  bash -c 'find /var/log -type f -exec truncate -s 0 {} + 2>/dev/null || true'

do_step "remove this unit's boot-partition logs" \
  bash -c "rm -f '$BOOT_DIR'/combiner-firstboot.log '$BOOT_DIR'/combiner-firstboot.log.prev '$BOOT_DIR'/combiner-apply.log"

do_step "clear shell histories and known_hosts" \
  bash -c 'rm -f /root/.bash_history /home/*/.bash_history /root/.ssh/known_hosts /home/*/.ssh/known_hosts 2>/dev/null || true'

# --- safety net -------------------------------------------------------------
# Raspberry Pi OS regenerates host keys from regenerate_ssh_host_keys.service,
# which is gated on ConditionFirstBoot. That condition did NOT fire on a sealed
# card in testing, leaving a unit with no host keys — and ssh.service runs
# `sshd -t` as its own ExecStartPre, which fails outright when keys are absent.
# So sshd never starts and the unit is unreachable.
#
# A drop-in cannot fix that: drop-in ExecStartPre= lines are APPENDED, so they
# run after the failing check. This needs its own unit, ordered before ssh, with
# no condition on it. `ssh-keygen -A` only creates what is missing, so running
# it on every boot is idempotent and costs nothing.
install_hostkey_unit() {
  cat >/etc/systemd/system/combiner-hostkeys.service <<'EOF'
[Unit]
Description=Generate any missing SSH host keys (combiner)
Documentation=https://github.com/misnow1/vunet-dante-combiner-2000/blob/main/docs/sd-image.md
After=systemd-remount-fs.service
Before=ssh.service sshd.service
ConditionPathIsReadWrite=/etc
ConditionFileIsExecutable=/usr/bin/ssh-keygen
DefaultDependencies=no
Conflicts=shutdown.target
Before=shutdown.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/ssh-keygen -A

[Install]
WantedBy=sysinit.target
EOF
  # An earlier version of this script shipped a drop-in that could never work.
  rm -f /etc/systemd/system/ssh.service.d/10-combiner-hostkeys.conf
  rmdir /etc/systemd/system/ssh.service.d 2>/dev/null || true
  systemctl daemon-reload
  systemctl enable combiner-hostkeys.service >/dev/null 2>&1
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "  would: install and enable combiner-hostkeys.service (ssh host-key safety net)"
else
  echo "  install and enable combiner-hostkeys.service (ssh host-key safety net)"
  install_hostkey_unit
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  say "--dry-run: nothing was changed"
  exit 0
fi

sync

cat <<EOF

Sealed. This unit now has no identity of its own.

Next:
  1. Power off (do NOT boot this card again unless you re-seal it).
  2. Image the card, e.g.  sudo dd if=/dev/diskN of=combiner-golden.img bs=4m
  3. Write the image to as many cards as you need.
  4. On EACH card's boot partition, put that rig's combiner-site.yaml
     (deploy/pi/prep-card.sh --site <rig>.yaml --check-card).

Each clone regenerates its own machine-id and SSH host keys on first boot, and
combiner-apply paves in whichever config its card carries.
EOF

if [[ "$POWEROFF" -eq 1 ]]; then
  say "powering off"
  systemctl poweroff
fi
