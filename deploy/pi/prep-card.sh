#!/usr/bin/env bash
# Stage combiner provisioning onto a freshly flashed Raspberry Pi OS card.
#
# Flash stock Raspberry Pi OS Lite with Raspberry Pi Imager first, then run this
# against the mounted boot partition. It validates the site config BEFORE the
# card is touched, so a typo is caught at the bench instead of on a dark unit in
# a rack. Windows users copy the same files by hand — see docs/sd-image.md.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLOUD_INIT="$ROOT/deploy/pi/cloud-init"
VERSION_DEFAULT="0.1.0"
REPO="misnow1/vunet-dante-combiner-2000"

CARD=""
SITE=""
SSH_KEY=""
USER_DATA=""
VERSION="$VERSION_DEFAULT"
STAGE_TARBALL=1
FORCE=0

usage() {
  cat <<'USAGE'
usage: prep-card.sh --site FILE [options]

  --site FILE        site config to install as combiner-site.yaml (required)
  --ssh-key FILE     public key to authorise, e.g. ~/.ssh/id_ed25519.pub
                     (substituted into the shipped user-data template)
  --user-data FILE   use your own already-edited user-data instead of --ssh-key
  --card DIR         mounted boot partition (default: autodetect)
  --version V        release to stage/pin (default: the version this tree ships)
  --no-tarball       do not stage a release tarball; the Pi downloads it on
                     first boot, which then requires Internet at the bench
  --force            skip config validation (not recommended)
  -h, --help         this text

example:
  ./deploy/pi/prep-card.sh --site my-venue.yaml --ssh-key ~/.ssh/id_ed25519.pub
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --site)      SITE="${2:?--site needs a file}"; shift 2 ;;
    --ssh-key)   SSH_KEY="${2:?--ssh-key needs a file}"; shift 2 ;;
    --user-data) USER_DATA="${2:?--user-data needs a file}"; shift 2 ;;
    --card)      CARD="${2:?--card needs a directory}"; shift 2 ;;
    --version)   VERSION="${2:?--version needs a value}"; shift 2 ;;
    --no-tarball) STAGE_TARBALL=0; shift ;;
    --force)     FORCE=1; shift ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }

[[ -n "$SITE" ]] || { usage >&2; die "--site is required"; }
[[ -f "$SITE" ]] || die "no such site config: $SITE"
VERSION="${VERSION#v}"

# --- locate the boot partition ---------------------------------------------
# Imager labels the FAT partition "bootfs" (older images: "boot").
if [[ -z "$CARD" ]]; then
  for c in /Volumes/bootfs /Volumes/boot \
           "/media/$USER/bootfs" "/media/$USER/boot" \
           "/run/media/$USER/bootfs" "/run/media/$USER/boot"; do
    [[ -d "$c" ]] && { CARD="$c"; break; }
  done
  [[ -n "$CARD" ]] || die "could not find a mounted boot partition — pass --card DIR
(flash the card with Raspberry Pi Imager first, then re-insert it)"
  echo "card: $CARD (autodetected)"
else
  [[ -d "$CARD" ]] || die "no such directory: $CARD"
  echo "card: $CARD"
fi

# Never scribble on a volume that is not a Pi boot partition.
if [[ ! -f "$CARD/config.txt" && ! -f "$CARD/cmdline.txt" ]]; then
  die "$CARD does not look like a Raspberry Pi boot partition
(expected config.txt or cmdline.txt). Refusing to write to it."
fi
[[ -w "$CARD" ]] || die "$CARD is not writable"

# --- validate the site config before touching the card ----------------------
find_combiner() {
  if [[ -x "$ROOT/bin/combiner" ]]; then echo "$ROOT/bin/combiner"; return 0; fi
  if command -v combiner >/dev/null 2>&1; then command -v combiner; return 0; fi
  if command -v go >/dev/null 2>&1; then
    local tmp
    tmp="$(mktemp -d)"
    if (cd "$ROOT" && go build -o "$tmp/combiner" ./cmd/combiner) >/dev/null 2>&1; then
      echo "$tmp/combiner"; return 0
    fi
  fi
  return 1
}

if [[ "$FORCE" -eq 1 ]]; then
  echo "warning: --force — skipping config validation" >&2
elif COMBINER_BIN="$(find_combiner)"; then
  # Validate against a staged copy so allowlist_files resolve the way they will
  # on the Pi, where install.sh puts allowlists next to site.yaml.
  STAGE="$(mktemp -d)"
  trap 'rm -rf "$STAGE"' EXIT
  mkdir -p "$STAGE/allowlists"
  cp "$SITE" "$STAGE/site.yaml"
  cp -a "$ROOT/config/allowlists/." "$STAGE/allowlists/"
  echo "validating $SITE"
  "$COMBINER_BIN" -check -config "$STAGE/site.yaml" ||
    die "site config failed validation — fix it before writing the card"
else
  die "no combiner binary to validate with (looked in bin/, PATH, and tried go build)
build one with 'make build', or re-run with --force to skip validation"
fi

# --- assemble user-data -----------------------------------------------------
PLACEHOLDER="REPLACE_WITH_YOUR_SSH_PUBLIC_KEY"
STAGED_USER_DATA="$(mktemp)"
trap 'rm -rf "${STAGE:-}" "$STAGED_USER_DATA"' EXIT

if [[ -n "$USER_DATA" ]]; then
  [[ -f "$USER_DATA" ]] || die "no such user-data: $USER_DATA"
  cp "$USER_DATA" "$STAGED_USER_DATA"
elif [[ -n "$SSH_KEY" ]]; then
  [[ -f "$SSH_KEY" ]] || die "no such key file: $SSH_KEY"
  grep -qE '^(ssh|ecdsa)-' "$SSH_KEY" ||
    die "$SSH_KEY does not look like a PUBLIC key (expected it to start with ssh-… )
did you mean $SSH_KEY.pub?"
  KEY_LINE="$(head -n 1 "$SSH_KEY")"
  # awk, not sed: a key is base64 and can contain '/' and '&'.
  awk -v key="$KEY_LINE" -v ph="$PLACEHOLDER" \
    '{ if (index($0, ph)) { sub(ph, key) } print }' \
    "$CLOUD_INIT/user-data" >"$STAGED_USER_DATA"
else
  cp "$CLOUD_INIT/user-data" "$STAGED_USER_DATA"
fi

if grep -q "$PLACEHOLDER" "$STAGED_USER_DATA"; then
  echo "warning: user-data still contains the SSH key placeholder." >&2
  echo "         This unit will come up console-only — nothing can SSH in." >&2
  echo "         Pass --ssh-key FILE, or --user-data with your own file." >&2
fi

# --- stage the release tarball ----------------------------------------------
if [[ "$STAGE_TARBALL" -eq 1 ]]; then
  BASE="https://github.com/$REPO/releases/download/v$VERSION"
  for arch in arm64 arm; do
    name="vunet-dante-combiner-$VERSION-linux-$arch.tar.gz"
    if [[ -f "$CARD/$name" ]]; then
      echo "already staged: $name"
      continue
    fi
    echo "downloading $name"
    curl -fL --retry 3 --progress-bar -o "$CARD/$name" "$BASE/$name" || {
      rm -f "$CARD/$name"
      die "could not download $name
re-run with --no-tarball to let the Pi fetch it on first boot instead"
    }
  done
  echo "downloading SHA256SUMS"
  curl -fsSL --retry 3 -o "$CARD/SHA256SUMS" "$BASE/SHA256SUMS" ||
    echo "warning: no SHA256SUMS — the Pi will skip checksum verification" >&2
else
  echo "--no-tarball: the Pi will download the release on first boot"
fi

# --- write the provisioning files -------------------------------------------
install -m 0644 "$STAGED_USER_DATA" "$CARD/user-data"
install -m 0644 "$SITE"             "$CARD/combiner-site.yaml"
install -m 0644 "$CLOUD_INIT/combiner-firstboot.sh" "$CARD/combiner-firstboot.sh"

# Imager writes meta-data itself; only supply one if it did not, since NoCloud
# needs it present to treat the partition as a datasource.
if [[ -f "$CARD/meta-data" ]]; then
  echo "keeping Imager's meta-data"
else
  install -m 0644 "$CLOUD_INIT/meta-data" "$CARD/meta-data"
  echo "wrote meta-data (Imager had not)"
fi

# A log from a previous unit built on this card would be read as this one's.
rm -f "$CARD/combiner-firstboot.log"

sync 2>/dev/null || true

cat <<EOF

Card ready.

  $CARD/user-data
  $CARD/combiner-site.yaml
  $CARD/combiner-firstboot.sh

Eject the card, boot the Pi with a network it can reach, and wait for the first
boot to finish. Then check $(basename "$CARD")/combiner-firstboot.log on the card, or run
'combiner-status' on the box.
EOF
