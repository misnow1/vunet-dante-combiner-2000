#!/usr/bin/env bash
# Arm (or release) the read-only root on a single unit.
#
# combiner-seal is for cards that will be CLONED: it strips the unit's identity
# so each copy generates its own. A unit going into a rack as itself needs none
# of that — it just needs the root locked. That took two undocumented steps
# (apt-get install overlayroot; systemctl enable combiner-finalize), which is
# how this script came to exist.
#
# Locking still happens at the next boot, via combiner-finalize: see that script
# for why it cannot happen here.
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"
CMDLINE="$BOOT_DIR/cmdline.txt"

ACTION="arm"
DO_REBOOT=0

usage() {
  cat <<'USAGE'
usage: combiner-lock [--reboot]
       combiner-lock --off [--reboot]
       combiner-lock --status

  (default)   arm the read-only root; it engages on the next boot
  --off       release it; the root is writable again after the next boot
  --status    report the current state and exit
  --reboot    reboot when done
  -h, --help  this text
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --off)     ACTION="off"; shift ;;
    --status)  ACTION="status"; shift ;;
    --reboot)  DO_REBOOT=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "combiner-lock: $*" >&2; exit 1; }
say() { echo "combiner-lock: $*"; }

locked_now()   { grep -q 'overlayroot=tmpfs' /proc/cmdline; }
locked_next()  { grep -q 'overlayroot=tmpfs' "$CMDLINE" 2>/dev/null; }

if [[ "$ACTION" == "status" ]]; then
  echo "  root now:        $(findmnt -no FSTYPE /) ($(locked_now && echo read-only || echo writable))"
  echo "  next boot:       $(locked_next && echo read-only || echo writable)"
  echo "  finalize armed:  $(systemctl is-enabled combiner-finalize.service 2>&1)"
  echo "  overlayroot:     $(dpkg -s overlayroot >/dev/null 2>&1 && echo installed || echo not installed)"
  exit 0
fi

[[ "$(id -u)" -eq 0 ]] || die "run as root"

if [[ "$ACTION" == "off" ]]; then
  if locked_next; then
    [[ -w "$BOOT_DIR" ]] || die "$BOOT_DIR is not writable"
    cp "$CMDLINE" "$CMDLINE.combiner-prelock" 2>/dev/null || true
    TMP="$(mktemp)"
    sed -e 's/overlayroot=tmpfs //g' "$CMDLINE" >"$TMP"
    [[ "$(wc -l <"$TMP")" -le 1 ]] || { rm -f "$TMP"; die "refusing to write a multi-line cmdline.txt"; }
    cat "$TMP" >"$CMDLINE"; rm -f "$TMP"; sync
    say "read-only root released; the root is writable after the next boot"
  else
    say "the root is already set to be writable on the next boot"
  fi
  # When the root is writable this disable persists. When it is NOT, finalize
  # already disabled itself at lock time, so there is nothing left to do — the
  # disable is in the read-only lower layer where it belongs.
  if ! locked_now; then
    systemctl disable combiner-finalize.service >/dev/null 2>&1 || true
  fi
  [[ "$DO_REBOOT" -eq 1 ]] && { say "rebooting"; systemctl reboot; }
  exit 0
fi

# --- arm --------------------------------------------------------------------
if locked_now || locked_next; then
  say "already locked (or locking on the next boot) — nothing to do"
  exit 0
fi

# Fail here, at a bench with a person watching, rather than silently at boot.
[[ -s /etc/machine-id ]] || die "machine-id is empty — this looks like a sealed card.
Boot it once so it generates an identity, or use combiner-seal for a card to clone."
shopt -s nullglob
keys=(/etc/ssh/ssh_host_*_key)
shopt -u nullglob
[[ ${#keys[@]} -gt 0 ]] || die "no SSH host keys — locking now would freeze a unit with none"

[[ -f /etc/combiner/site.yaml ]] || die "no /etc/combiner/site.yaml — run install.sh first"
command -v combiner >/dev/null 2>&1 || die "combiner is not installed — run install.sh first"
combiner -check -config /etc/combiner/site.yaml >/dev/null ||
  die "the installed config does not validate — fix it before locking"

# No apt here on purpose: this may run on a unit already in a rack, with no
# network. overlayroot is installed during provisioning (see user-data).
dpkg -s overlayroot >/dev/null 2>&1 ||
  die "overlayroot is not installed. It ships as a provisioning dependency, so
this unit predates that change. On a machine with a network:

    sudo apt-get install overlayroot"

[[ -x /usr/local/sbin/combiner-finalize ]] ||
  die "combiner-finalize is not installed — re-run install.sh from a release tree"
systemctl enable combiner-finalize.service >/dev/null 2>&1 ||
  die "could not enable combiner-finalize.service"

say "armed. The next boot locks the root read-only and reboots once more."
say "Release it later with: combiner-lock --off"
[[ "$DO_REBOOT" -eq 1 ]] && { say "rebooting"; systemctl reboot; }
exit 0
