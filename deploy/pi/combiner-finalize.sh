#!/usr/bin/env bash
# Lock this unit's root filesystem read-only, once — and only once — it has an
# identity of its own and a working configuration.
#
# Why this is a boot-time state machine rather than something combiner-seal
# does directly: a sealed card has NO identity (see combiner-seal). A clone
# generates its machine-id and SSH host keys on first boot, and under a
# read-only root those writes land in the tmpfs upper layer and are discarded
# at every reboot — so the unit would present a different host key every time.
# The first boot therefore has to run writable. This service waits for the
# identity to exist, locks the root, and reboots into the locked state.
#
# Undo, from the box:      sudo raspi-config nonint disable_overlayfs && sudo reboot
# Undo, from a laptop:     remove `overlayroot=tmpfs` from cmdline.txt on the card
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"
CMDLINE="$BOOT_DIR/cmdline.txt"

say() { echo "combiner-finalize: $*"; }

# Everything here is diagnosable from the card, like the other boot-time steps.
if [[ -d "$BOOT_DIR" && -w "$BOOT_DIR" ]]; then
  exec > >(tee -a "$BOOT_DIR/combiner-finalize.log") 2>&1
fi

disable_self() { systemctl disable combiner-finalize.service >/dev/null 2>&1 || true; }

# --- already locked? --------------------------------------------------------
if grep -q 'overlayroot=tmpfs' /proc/cmdline; then
  say "root is already read-only — nothing to do"
  disable_self
  exit 0
fi

# --- does this unit have an identity of its own yet? ------------------------
# A clone inherits an empty machine-id and no host keys from the sealed image.
# Locking before they are generated would freeze the unit without them.
if [[ ! -s /etc/machine-id ]]; then
  say "machine-id not yet generated — leaving root writable, will retry next boot"
  exit 0
fi
shopt -s nullglob
HOST_KEYS=(/etc/ssh/ssh_host_*_key)
shopt -u nullglob
if [[ ${#HOST_KEYS[@]} -eq 0 ]]; then
  say "no SSH host keys yet — leaving root writable, will retry next boot"
  exit 0
fi

# --- is the unit actually working? ------------------------------------------
# Freezing a misconfigured box makes it harder to fix, so only lock one whose
# config actually applied.
if ! systemctl is-active --quiet combiner-apply.service; then
  say "combiner-apply has not succeeded — refusing to lock a unit that is not configured"
  say "fix the config on the boot partition; this will retry on the next boot"
  exit 0
fi

# --- can we lock offline? ---------------------------------------------------
# combiner-seal installs overlayroot at bench time precisely so this step needs
# no network. If it is missing we cannot lock, and must not try to apt-get it
# on a unit that may be in a rack.
if ! dpkg -s overlayroot >/dev/null 2>&1; then
  say "overlayroot is not installed — cannot lock the root filesystem"
  say "install it where there is a network (apt-get install overlayroot), then re-seal"
  exit 0
fi

[[ -f "$CMDLINE" ]] || { say "no $CMDLINE — cannot lock"; exit 0; }

# Guard the FILE as well as /proc/cmdline. If a previous run wrote the token but
# the reboot did not happen, prepending again would give two copies — which
# raspi-config's disable_overlayfs (a single-occurrence sed) would not fully
# undo, leaving a unit that silently stays read-only after being unlocked.
if grep -q 'overlayroot=tmpfs' "$CMDLINE"; then
  say "cmdline.txt already requests a read-only root — takes effect on next boot"
  disable_self
  exit 0
fi
if [[ ! -w "$BOOT_DIR" ]]; then
  say "$BOOT_DIR is not writable — cannot lock"
  exit 0
fi

# --- lock it ----------------------------------------------------------------
say "identity present and config applied — locking root filesystem read-only"

# systemd-remount-fs remounts / from fstab, which overlayfs rejects outright
# ("No changes allowed in reconfigure"). Left alone it becomes a permanently
# failed unit, which makes `systemctl --failed` useless as a health signal on a
# box nobody can easily inspect. Skip it while the overlay is active.
#
# Written now, while the root is still writable, so it persists. The condition
# lifts by itself if the root is ever unlocked — overlayroot only rewrites fstab
# in the tmpfs upper layer, so the card's own fstab stays valid.
# Under the overlay every journal write lands in the tmpfs upper layer, where
# an unbounded journal quietly eats RAM. Point journald at /run explicitly and
# cap it. Not done at install time on purpose: a bench unit that is still
# writable should keep a persistent journal to debug with.
mkdir -p /etc/systemd/journald.conf.d
cat >/etc/systemd/journald.conf.d/10-combiner-volatile.conf <<'EOF'
# Installed by combiner-finalize alongside the read-only root.
[Journal]
Storage=volatile
RuntimeMaxUse=32M
EOF

mkdir -p /etc/systemd/system/systemd-remount-fs.service.d
cat >/etc/systemd/system/systemd-remount-fs.service.d/10-combiner-overlay.conf <<'EOF'
# Installed by combiner-finalize. Under a read-only overlay root there is
# nothing for this unit to remount, and attempting it fails.
[Unit]
ConditionKernelCommandLine=!overlayroot=tmpfs
EOF
systemctl daemon-reload
cp "$CMDLINE" "$CMDLINE.combiner-prelock"

TMP="$(mktemp)"
sed -e 's/^/overlayroot=tmpfs /' "$CMDLINE" >"$TMP"
# cmdline.txt must remain a single line; a mangled one is an unbootable Pi.
if [[ "$(wc -l <"$TMP")" -gt 1 ]] || ! grep -q 'overlayroot=tmpfs' "$TMP"; then
  rm -f "$TMP"
  say "refusing to write a malformed cmdline.txt — root left writable"
  exit 1
fi
cat "$TMP" >"$CMDLINE"
rm -f "$TMP"
sync

disable_self
say "locked. rebooting into read-only root"
systemctl reboot
