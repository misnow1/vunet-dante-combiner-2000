#!/usr/bin/env bash
# First-boot provisioning, run once by cloud-init (see user-data next to this
# file). Installs the combiner from a release tarball and hands off to
# install.sh, which stays the single tested install path.
#
# Everything is logged to the FAT boot partition rather than only to
# /var/log/cloud-init-output.log, because the recovery move for a headless unit
# that did not come up is to pull the card and read it on a laptop — which
# cannot mount the ext4 root.
set -euo pipefail

# Pinned so a rebuild of the same card produces the same unit. prep-card.sh
# stages a tarball on the card by default, and a staged tarball always wins, so
# this only matters when the Pi downloads its own.
COMBINER_VERSION="${COMBINER_VERSION:-0.2.1}"
REPO="misnow1/vunet-dante-combiner-2000"

BOOT="/boot/firmware"
[[ -d "$BOOT" ]] || BOOT="/boot"

LOG="$BOOT/combiner-firstboot.log"
MARKER="/var/lib/combiner/provisioned"
INSTALL_ROOT="/opt/combiner"

exec > >(tee -a "$LOG") 2>&1

say() { echo "[combiner-firstboot] $*"; }

fail() {
  echo ""
  echo "==================== PROVISIONING FAILED ===================="
  echo "$*"
  echo ""
  echo "The unit has been left with IP forwarding OFF, which is the safe"
  echo "state: it will not bridge Control and Dante until this is fixed."
  echo ""
  echo "Read this file by putting the card in a laptop — it is on the FAT"
  echo "boot partition, so macOS and Windows can both see it:"
  echo "  $(basename "$LOG")"
  echo "============================================================"
  exit 1
}

say "started $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

if [[ -e "$MARKER" ]]; then
  say "already provisioned ($MARKER) — nothing to do"
  exit 0
fi

# --- site config ------------------------------------------------------------
SITE_SRC="$BOOT/combiner-site.yaml"
if [[ ! -f "$SITE_SRC" ]]; then
  fail "missing $SITE_SRC

Copy a profile from the release's config/ directory onto the boot partition
as combiner-site.yaml and edit it for this site. See docs/sd-image.md."
fi

# --- pick the release matching this OS --------------------------------------
case "$(dpkg --print-architecture 2>/dev/null || echo unknown)" in
  arm64) ARCH_SUFFIX=arm64 ;;
  armhf) ARCH_SUFFIX=arm ;;
  amd64) ARCH_SUFFIX=amd64 ;;
  *) fail "unsupported architecture: $(dpkg --print-architecture 2>/dev/null || uname -m)" ;;
esac
say "architecture: $ARCH_SUFFIX"

# A tarball staged on the card wins, so a bench-prepped unit never needs a
# network on first boot.
shopt -s nullglob
STAGED=("$BOOT"/vunet-dante-combiner-*-linux-"$ARCH_SUFFIX".tar.gz)
shopt -u nullglob

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

if [[ ${#STAGED[@]} -gt 0 ]]; then
  TARBALL="${STAGED[0]}"
  say "using staged tarball: $(basename "$TARBALL")"
  if [[ ${#STAGED[@]} -gt 1 ]]; then
    say "warning: ${#STAGED[@]} staged tarballs for $ARCH_SUFFIX, using the first"
  fi
else
  URL="https://github.com/$REPO/releases/download/v$COMBINER_VERSION/vunet-dante-combiner-$COMBINER_VERSION-linux-$ARCH_SUFFIX.tar.gz"
  say "no staged tarball — downloading $URL"
  TARBALL="$WORK/combiner.tar.gz"
  curl -fsSL --retry 3 --connect-timeout 20 -o "$TARBALL" "$URL" || fail "download failed: $URL

This Pi has no route to GitHub. Either give it Internet for the first boot, or
stage the tarball on the boot partition next to combiner-site.yaml (which is
what deploy/pi/prep-card.sh does by default)."
fi

# --- verify, if a checksum file was staged alongside ------------------------
if [[ -f "$BOOT/SHA256SUMS" ]]; then
  # --status: the exit code is the verdict. Piping to `grep -q ": OK"` is wrong
  # twice over — it passes whenever some OTHER staged file verified, and grep
  # exiting on the first match SIGPIPEs sha256sum, which `pipefail` then reports
  # as failure even when every checksum was good.
  if (cd "$(dirname "$TARBALL")" && sha256sum -c --ignore-missing --status "$BOOT/SHA256SUMS"); then
    say "checksum OK"
  else
    fail "checksum mismatch for $(basename "$TARBALL") against $BOOT/SHA256SUMS

The tarball on the card is corrupt or does not match the checksums. Re-stage
the card rather than installing an unverified build."
  fi
else
  say "no SHA256SUMS staged — skipping checksum verification"
fi

# --- unpack -----------------------------------------------------------------
say "unpacking"
tar -xzf "$TARBALL" -C "$WORK" || fail "could not unpack $TARBALL"

TREE="$(find "$WORK" -maxdepth 1 -type d -name 'vunet-dante-combiner-*' -print -quit)"
[[ -n "$TREE" && -x "$TREE/deploy/pi/install.sh" ]] ||
  fail "unpacked tree does not look like a release (no deploy/pi/install.sh)"

rm -rf "$INSTALL_ROOT"
mkdir -p "$(dirname "$INSTALL_ROOT")"
mv "$TREE" "$INSTALL_ROOT"
say "installed tree at $INSTALL_ROOT"

# --- site config into place -------------------------------------------------
mkdir -p /etc/combiner
cp "$SITE_SRC" /etc/combiner/site.yaml
say "copied $SITE_SRC -> /etc/combiner/site.yaml"

# --- hand off to the real installer -----------------------------------------
# No --i-have-console: cloud-init is not an SSH session, so install.sh's guard
# passes on its own. If that ever changes, the guard SHOULD stop us.
say "running install.sh"
if ! "$INSTALL_ROOT/deploy/pi/install.sh" /etc/combiner/site.yaml; then
  fail "install.sh failed — see the output above in this log"
fi

mkdir -p "$(dirname "$MARKER")"
date -u '+%Y-%m-%dT%H:%M:%SZ' >"$MARKER"

say "provisioning complete"
say "version: $("$INSTALL_ROOT/bin/combiner" -version 2>/dev/null || echo unknown)"
say ""
say "Status page: http://<control-ip>:8080/"
say "On the box:  combiner-status"
