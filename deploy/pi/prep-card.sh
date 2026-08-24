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
LOGIN_USER=""
CHECK_CARD=0
ASK_PASSWORD=0
PASSWORD_FILE=""
PASSWORD_HASH=""
VERSION="$VERSION_DEFAULT"
STAGE_TARBALL=1
FORCE=0

usage() {
  cat <<'USAGE'
usage: prep-card.sh --site FILE [options]

  --site FILE        site config to install as combiner-site.yaml (required)
  --ssh-key FILE     public key to authorise, e.g. ~/.ssh/id_ed25519.pub
                     (substituted into the shipped user-data template)
  --user NAME        login to create (default: combiner)
  --user-data FILE   use your own already-edited user-data instead of --ssh-key

  break-glass password (for a laptop with no SSH key on it) — pick one:
  --ask-password     prompt for it, twice, without echoing
  --password-file F  read it from the first line of F
  --password-hash H  use an already-hashed password verbatim
                     (or set COMBINER_PASSWORD in the environment)
  --card DIR         mounted boot partition (default: autodetect)
  --version V        release to stage/pin (default: the version this tree ships)
  --no-tarball       do not stage a release tarball; the Pi downloads it on
                     first boot, which then requires Internet at the bench
  --check-card       re-validate the combiner-site.yaml already on the card and
                     exit, changing nothing (run this after hand-editing it)
  --force            skip config validation (not recommended)
  -h, --help         this text

examples:
  ./deploy/pi/prep-card.sh --site my-venue.yaml --ssh-key ~/.ssh/id_ed25519.pub
  ./deploy/pi/prep-card.sh --site my-venue.yaml --ask-password
  ./deploy/pi/prep-card.sh --check-card
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --site)      SITE="${2:?--site needs a file}"; shift 2 ;;
    --ssh-key)   SSH_KEY="${2:?--ssh-key needs a file}"; shift 2 ;;
    --user)      LOGIN_USER="${2:?--user needs a name}"; shift 2 ;;
    --user-data) USER_DATA="${2:?--user-data needs a file}"; shift 2 ;;
    --ask-password) ASK_PASSWORD=1; shift ;;
    --password-file) PASSWORD_FILE="${2:?--password-file needs a file}"; shift 2 ;;
    --password-hash) PASSWORD_HASH="${2:?--password-hash needs a value}"; shift 2 ;;
    --card)      CARD="${2:?--card needs a directory}"; shift 2 ;;
    --version)   VERSION="${2:?--version needs a value}"; shift 2 ;;
    --no-tarball) STAGE_TARBALL=0; shift ;;
    --check-card) CHECK_CARD=1; shift ;;
    --force)     FORCE=1; shift ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { echo "error: $*" >&2; exit 1; }

if [[ "$CHECK_CARD" -eq 0 ]]; then
  [[ -n "$SITE" ]] || { usage >&2; die "--site is required"; }
  [[ -f "$SITE" ]] || die "no such site config: $SITE"
fi
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

validate_site_config() {
  local site="$1" bin stage
  bin="$(find_combiner)" || die "no combiner binary to validate with (looked in bin/, PATH,
and tried go build). Build one with 'make build', or re-run with --force."
  # Validate against a staged copy so allowlist_files resolve the way they will
  # on the Pi, where install.sh puts allowlists next to site.yaml.
  stage="$(mktemp -d)"
  mkdir -p "$stage/allowlists"
  cp "$site" "$stage/site.yaml"
  cp -a "$ROOT/config/allowlists/." "$stage/allowlists/"
  echo "validating $site"
  local rc=0
  "$bin" -check -config "$stage/site.yaml" || rc=$?
  rm -rf "$stage"
  return "$rc"
}

# --check-card: re-check what is already staged and stop. This is the gap that
# hand-editing combiner-site.yaml on the card opens — prep-card.sh validated
# the source file, but nothing re-reads the card before the Pi boots.
if [[ "$CHECK_CARD" -eq 1 ]]; then
  CARD_SITE="$CARD/combiner-site.yaml"
  [[ -f "$CARD_SITE" ]] || die "no combiner-site.yaml on $CARD — stage the card first"
  if validate_site_config "$CARD_SITE"; then
    echo ""
    echo "OK: $CARD_SITE would be accepted by install.sh"
    exit 0
  fi
  die "the config on the card would be REJECTED on first boot — fix it before booting"
fi

if [[ "$FORCE" -eq 1 ]]; then
  echo "warning: --force — skipping config validation" >&2
else
  validate_site_config "$SITE" ||
    die "site config failed validation — fix it before writing the card"
fi

# --- break-glass password ---------------------------------------------------
# Optional: lets someone log in from a laptop that has no SSH key on it.
# Never goes near argv (ps is world-readable) — everything is piped on stdin.
# A real SHA-512 crypt hash: $6$[rounds=N$]salt$<86 chars>. macOS's crypt(3)
# does not implement $6$ and quietly returns a short DES value instead, which
# would install a break-glass password that cannot be used — discovered in the
# rack, which is exactly what this feature exists to avoid. Shape-check every
# hash before it can reach a card.
valid_sha512_crypt() {
  [[ "$1" =~ ^\$6\$(rounds=[0-9]+\$)?[./A-Za-z0-9]{1,16}\$[./A-Za-z0-9]{86}$ ]]
}

hash_password() {
  local pw="$1" out="" ossl
  # Stock macOS ships LibreSSL, whose `passwd` has no -6, so probe rather than
  # assume; a Homebrew OpenSSL may be installed alongside it.
  for ossl in openssl /opt/homebrew/bin/openssl \
              /opt/homebrew/opt/openssl@3/bin/openssl \
              /usr/local/opt/openssl@3/bin/openssl; do
    command -v "$ossl" >/dev/null 2>&1 || continue
    printf 'probe\n' | "$ossl" passwd -6 -stdin >/dev/null 2>&1 || continue
    out="$(printf '%s\n' "$pw" | "$ossl" passwd -6 -stdin 2>/dev/null)" || continue
    valid_sha512_crypt "$out" && { printf '%s\n' "$out"; return 0; }
  done
  # Python's crypt module was removed in 3.13 and is broken on macOS; the shape
  # check above is what makes trying it safe.
  if command -v python3 >/dev/null 2>&1; then
    out="$(printf '%s\n' "$pw" | python3 -c 'import crypt, sys
pw = sys.stdin.readline().rstrip("\n")
print(crypt.crypt(pw, crypt.mksalt(crypt.METHOD_SHA512)))' 2>/dev/null)" || true
    valid_sha512_crypt "$out" && { printf '%s\n' "$out"; return 0; }
  fi
  return 1
}

PASSWORD=""
HAVE_PW=0
if [[ -n "$PASSWORD_HASH" ]]; then
  valid_sha512_crypt "$PASSWORD_HASH" ||
    die "--password-hash is not a SHA-512 crypt hash (expected \$6\$salt\$… , 86 trailing chars).
If you pasted the password itself, use --ask-password instead — a plaintext
password here would be written to the card and would not work as a login."
  HAVE_PW=1
elif [[ "$ASK_PASSWORD" -eq 1 ]]; then
  [[ -t 0 ]] || die "--ask-password needs a terminal — use --password-file or COMBINER_PASSWORD"
  read -rsp "Break-glass password: " PASSWORD; echo
  read -rsp "Confirm password:     " PASSWORD_CONFIRM; echo
  [[ "$PASSWORD" == "$PASSWORD_CONFIRM" ]] || die "passwords did not match"
  unset PASSWORD_CONFIRM
  HAVE_PW=1
elif [[ -n "$PASSWORD_FILE" ]]; then
  [[ -f "$PASSWORD_FILE" ]] || die "no such password file: $PASSWORD_FILE"
  # Strip CR so a file written on Windows does not smuggle one into the hash.
  PASSWORD="$(head -n 1 "$PASSWORD_FILE" | tr -d '\r')"
  HAVE_PW=1
elif [[ -n "${COMBINER_PASSWORD:-}" ]]; then
  PASSWORD="$COMBINER_PASSWORD"
  HAVE_PW=1
fi

if [[ "$HAVE_PW" -eq 1 && -z "$PASSWORD_HASH" ]]; then
  [[ -n "$PASSWORD" ]] || die "the password is empty"
  # The hash sits on a FAT partition that anyone holding the card can read, so
  # it is crackable offline. Short ones are not worth writing.
  [[ ${#PASSWORD} -ge 8 ]] ||
    die "password must be at least 8 characters — its hash ends up on the card's
boot partition, where anyone holding the card can attack it offline"
  PASSWORD_HASH="$(hash_password "$PASSWORD")" || die "no password hasher on this machine.
Install OpenSSL 3 (brew install openssl), or hash it somewhere that can — any
Linux box or the Pi itself — and pass the result:

    openssl passwd -6
    ./deploy/pi/prep-card.sh ... --password-hash '<the \$6\$... string>'"
  unset PASSWORD
fi

# --- assemble user-data -----------------------------------------------------
PLACEHOLDER="REPLACE_WITH_YOUR_SSH_PUBLIC_KEY"
PW_PLACEHOLDER="REPLACE_WITH_PASSWORD_HASH"
STAGED_USER_DATA="$(mktemp)"
chmod 600 "$STAGED_USER_DATA"
trap 'rm -rf "${STAGE:-}" "$STAGED_USER_DATA"' EXIT

SRC="$CLOUD_INIT/user-data"
if [[ -n "$USER_DATA" ]]; then
  [[ -f "$USER_DATA" ]] || die "no such user-data: $USER_DATA"
  SRC="$USER_DATA"
fi

if [[ -n "$LOGIN_USER" ]]; then
  # Interpolated straight into user-data, so a name that is not a plain Linux
  # username could break the YAML as well as fail useradd.
  [[ "$LOGIN_USER" =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] ||
    die "--user must be a lowercase Linux username (letters, digits, _ and -,
starting with a letter or underscore, at most 32 characters): got '$LOGIN_USER'"
fi
HAVE_USER=0
[[ -n "$LOGIN_USER" ]] && HAVE_USER=1

KEY_LINE=""
HAVE_KEY=0
if [[ -n "$SSH_KEY" ]]; then
  [[ -f "$SSH_KEY" ]] || die "no such key file: $SSH_KEY"
  grep -qE '^(ssh|ecdsa)-' "$SSH_KEY" ||
    die "$SSH_KEY does not look like a PUBLIC key (expected it to start with ssh-… )
did you mean $SSH_KEY.pub?"
  KEY_LINE="$(head -n 1 "$SSH_KEY")"
  HAVE_KEY=1
fi

# awk, not sed: a public key is base64 (contains '/' and '&') and a crypt hash
# is full of '$'. The ssh_authorized_keys header is held back so it can be
# dropped together with its placeholder when no key was given — an unreplaced
# placeholder would otherwise be installed as a literal, bogus key.
awk -v key="$KEY_LINE" -v havekey="$HAVE_KEY" \
    -v hash="$PASSWORD_HASH" -v havepw="$HAVE_PW" \
    -v user="$LOGIN_USER" -v haveuser="$HAVE_USER" \
    -v kph="$PLACEHOLDER" -v pph="$PW_PLACEHOLDER" -v q="'" '
  haveuser == "1" && !userdone && /^[[:space:]]*-[[:space:]]*name:[[:space:]]/ {
    print "  - name: " user; userdone = 1; next
  }
  /^[[:space:]]*ssh_authorized_keys:[[:space:]]*$/ { held = $0; next }
  index($0, kph) {
    if (havekey == "1") {
      line = $0; sub(kph, key, line)
      if (held != "") print held
      print line
    }
    held = ""; next
  }
  havepw == "1" && index($0, pph)                                  { print "    passwd: " q hash q; next }
  havepw == "1" && /^[[:space:]]*lock_passwd:[[:space:]]*true[[:space:]]*$/ { print "    lock_passwd: false"; next }
  havepw == "1" && /^ssh_pwauth:[[:space:]]*false[[:space:]]*$/    { print "ssh_pwauth: true"; next }
  { if (held != "") { print held; held = "" } print }
  END { if (held != "") print held }
' "$SRC" >"$STAGED_USER_DATA"

if [[ "$HAVE_PW" -eq 1 ]]; then
  echo "break-glass password set (SSH password auth enabled)"
fi
if [[ "$HAVE_KEY" -eq 0 && "$HAVE_PW" -eq 0 ]]; then
  echo "warning: no SSH key and no password — this unit will come up with no" >&2
  echo "         way to log in except a console. Pass --ssh-key and/or" >&2
  echo "         --ask-password, or --user-data with your own file." >&2
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

# cloud-init runs per-instance modules — users, runcmd, everything that
# provisions this box — exactly ONCE per instance-id, and Imager's meta-data
# pins a fixed one. Re-staging a card that has already booted would then be
# silently inert: no runcmd, no provisioning, no log, while SSH still works off
# the marker above and the old account lingers. So stamp a fresh id on every
# stage; that is what makes re-staging mean "provision again".
INSTANCE_ID="combiner-$(date -u +%Y%m%d%H%M%S)-$$"
printf 'instance-id: %s\nlocal-hostname: combiner\n' "$INSTANCE_ID" >"$CARD/meta-data"
chmod 644 "$CARD/meta-data"

# Raspberry Pi Imager also pins the instance-id on the KERNEL COMMAND LINE, as
# `ds=nocloud;i=<id>`, and that beats the meta-data file on the seed. Stamping
# meta-data alone is silently ineffective: cloud-init keeps seeing Imager's
# original id and never re-runs its per-instance modules, so a re-staged card
# looks staged but provisions nothing.
if [[ -f "$CARD/cmdline.txt" ]] && grep -q 'ds=nocloud' "$CARD/cmdline.txt"; then
  [[ -f "$CARD/cmdline.txt.combiner-orig" ]] ||
    cp "$CARD/cmdline.txt" "$CARD/cmdline.txt.combiner-orig"
  # cmdline.txt must stay a single line; rewrite just the i= token in place.
  CMDLINE_TMP="$(mktemp)"
  # Rewrite ONLY the i= inside the ds=nocloud parameter. Matching the first
  # bare "i=" on the line would be wrong: a cmdline can carry other parameters
  # ending in i= (the firmware appends several), and corrupting one of those
  # makes the Pi unbootable.
  awk -v id="$INSTANCE_ID" '{
    if (match($0, /ds=nocloud[^ ]*/)) {
      ds = substr($0, RSTART, RLENGTH)
      if (sub(/;i=[^ ;]*/, ";i=" id, ds) == 0) ds = ds ";i=" id
      print substr($0, 1, RSTART - 1) ds substr($0, RSTART + RLENGTH)
    } else {
      print
    }
  }' "$CARD/cmdline.txt" >"$CMDLINE_TMP"
  if [[ "$(wc -l <"$CMDLINE_TMP")" -gt 1 ]]; then
    rm -f "$CMDLINE_TMP"
    die "refusing to write a multi-line cmdline.txt"
  fi
  cat "$CMDLINE_TMP" >"$CARD/cmdline.txt"
  rm -f "$CMDLINE_TMP"
  echo "cmdline.txt: pinned instance-id updated to match"
fi

echo "meta-data: instance-id $INSTANCE_ID (forces a full re-provision)"

# Belt and braces with the runcmd in user-data: Raspberry Pi OS's sshswitch
# service enables sshd when this marker exists, which covers a boot that never
# reaches runcmd at all.
touch "$CARD/ssh"

# A log from a previous unit built on this card would be read as this one's,
# but deleting it destroys evidence from a boot you may still be debugging.
# Keep exactly one generation instead.
if [[ -f "$CARD/combiner-firstboot.log" ]]; then
  mv -f "$CARD/combiner-firstboot.log" "$CARD/combiner-firstboot.log.prev"
  echo "previous boot log kept as combiner-firstboot.log.prev"
fi

sync 2>/dev/null || true

cat <<EOF

Card ready.

  $CARD/user-data          (login: ${LOGIN_USER:-combiner})
  $CARD/combiner-site.yaml
  $CARD/combiner-firstboot.sh

Eject the card, boot the Pi with a network it can reach, and wait for the first
boot to finish. Then check $(basename "$CARD")/combiner-firstboot.log on the card, or run
'combiner-status' on the box.
EOF
