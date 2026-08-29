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
ARM_OVERLAY=1

usage() {
  cat <<'USAGE'
usage: combiner-seal [--yes] [--dry-run] [--poweroff]

  --yes        proceed without the confirmation prompt
  --dry-run    list what would be cleared; change nothing
  --poweroff   power off when done, so the card can be pulled and imaged
  --no-overlay do not arm the read-only root. Default is to arm it: each clone
               locks its root after its first boot, once it has an identity
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
    --no-overlay) ARM_OVERLAY=0; shift ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "combiner-seal: $*" >&2; exit 1; }
say() { echo "combiner-seal: $*"; }

[[ "$(id -u)" -eq 0 ]] || die "run as root"

# Refuse on a unit whose root is locked read-only. Every step below writes to
# /etc or /var — truncating machine-id, removing host keys and the random seed,
# removing site.yaml. Under an overlay those all land in tmpfs: seal would print
# its whole success output, tell you to image the card, and every change would
# evaporate on reboot. The card you then cloned would carry an identity that was
# never cleared, giving a fleet one machine-id and one set of SSH host keys —
# exactly what this script exists to prevent, while appearing to have worked.
if grep -q 'overlayroot=tmpfs' /proc/cmdline 2>/dev/null; then
  die "this unit's root is read-only, so nothing written here would survive a reboot.
Release it first, then seal:

    combiner-lock --off
    sudo reboot
    sudo combiner-seal"
fi

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
  # An earlier version shipped an ExecStartPre drop-in that could never work,
  # because drop-in ExecStartPre= lines are APPENDED — after ssh.service's own
  # `sshd -t`, which has already failed. Remove it if a card still carries one.
  rm -f /etc/systemd/system/ssh.service.d/10-combiner-hostkeys.conf

  # A drop-in IS the right tool for [Unit] dependencies, which merge additively:
  # this makes ssh.service pull the generator in rather than relying on
  # sysinit.target ordering alone.
  mkdir -p /etc/systemd/system/ssh.service.d
  cat >/etc/systemd/system/ssh.service.d/10-combiner-wants-hostkeys.conf <<'EOF'
# Installed by combiner-seal: sshd cannot start without host keys, so make it
# depend on the unit that guarantees they exist.
[Unit]
Wants=combiner-hostkeys.service
After=combiner-hostkeys.service
EOF
  systemctl daemon-reload
  systemctl enable combiner-hostkeys.service >/dev/null 2>&1
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "  would: install and enable combiner-hostkeys.service (ssh host-key safety net)"
else
  echo "  install and enable combiner-hostkeys.service (ssh host-key safety net)"
  install_hostkey_unit
fi

# --- read-only root ---------------------------------------------------------
# Arm it rather than enable it. A sealed card has no identity, and a clone
# generates machine-id and host keys on ITS first boot — under a read-only root
# those writes go to tmpfs and vanish, so the unit would present a different
# host key every reboot. combiner-finalize waits for the identity to exist,
# then locks the root and reboots.
#
# overlayroot is installed HERE, where there is a bench network, so the locking
# step itself needs none.
arm_overlay() {
  # overlayroot ships as a provisioning dependency (see user-data), so there is
  # no apt call here — sealing works on a unit with no network.
  dpkg -s overlayroot >/dev/null 2>&1 ||
    die "overlayroot is not installed. It ships as a provisioning dependency, so
this unit predates that change. Install it (apt-get install overlayroot) and
re-run, or pass --no-overlay to seal without arming the read-only root."
  # install.sh ships both; seal only decides whether they are armed.
  [[ -x /usr/local/sbin/combiner-finalize ]] ||
    die "combiner-finalize is not installed — re-run install.sh from a release tree"
  [[ -f /etc/systemd/system/combiner-finalize.service ]] ||
    die "combiner-finalize.service is not installed — re-run install.sh from a release tree"
  systemctl daemon-reload
  systemctl enable combiner-finalize.service >/dev/null 2>&1
}

if [[ "$ARM_OVERLAY" -eq 1 ]]; then
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "  would: install overlayroot and arm combiner-finalize (read-only root)"
  else
    echo "  arm read-only root (combiner-finalize locks it after the first boot)"
    arm_overlay
  fi
else
  echo "  read-only root NOT armed (--no-overlay)"
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
