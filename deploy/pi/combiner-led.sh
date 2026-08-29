#!/usr/bin/env bash
# Drive the Pi's activity LED as a rack-visible status signal.
#
# A racked, headless, read-only unit reports only via serial, the logs on the
# FAT boot partition, and the :8080 status page — all of which need something
# plugged in. The ACT LED is the one signal visible through a rack vent, so it
# carries the one thing an operator needs to know from across the room: is this
# unit still waiting for me, or is it running?
#
# Everything here is best-effort. There is no LED on an amd64 test host, and a
# unit whose LED cannot be driven must still provision, go live and run — so a
# missing LED is a silent success, never a failure.
set -euo pipefail

BOOT_DIR="/boot/firmware"
[[ -d "$BOOT_DIR" ]] || BOOT_DIR="/boot"
HOLD_MARKER="$BOOT_DIR/combiner-provisioning"

usage() {
  cat <<'USAGE'
usage: combiner-led STATE

  provisioning  fast heartbeat — first boot is working
  ready         slow steady blink — provisioned, awaiting combiner-go-live
  failed        rapid burst — provisioning or the service did not come up
  running       stock card-activity trigger — normal operation
  auto          pick from the unit's actual state (what combiner-signal runs)
  off           stop driving the LED, leave it dark

Best-effort by design: exits 0 when there is no LED to drive.
USAGE
}

STATE="${1:-}"
case "$STATE" in
  -h|--help) usage; exit 0 ;;
  "") usage >&2; exit 1 ;;
esac

# Validate the name BEFORE looking for hardware. A misspelled state is a caller
# bug on every machine, and most of the machines this is edited on have no LED
# at all — silently succeeding there is how it would reach a Pi.
case "$STATE" in
  provisioning|ready|failed|running|off|auto) ;;
  *) echo "combiner-led: unknown state: $STATE" >&2; usage >&2; exit 1 ;;
esac

if [[ "$STATE" == "auto" ]]; then
  if [[ -e "$HOLD_MARKER" ]]; then
    STATE="ready"
  elif systemctl is-active --quiet combiner 2>/dev/null; then
    STATE="running"
  else
    STATE="failed"
  fi
fi

# Pi 4/5 name it ACT; older kernels and some models expose led0. Take the first
# that exists rather than guessing from the model.
LED=""
for d in /sys/class/leds/ACT /sys/class/leds/led0; do
  [[ -d "$d" && -w "$d/trigger" ]] && { LED="$d"; break; }
done

# Not an error: no LED, or not root. Say so on stderr for a human running this
# by hand, and succeed, because every caller is on a path that must not fail.
if [[ -z "$LED" ]]; then
  echo "combiner-led: no writable activity LED — nothing to signal ($STATE)" >&2
  exit 0
fi

# Individual sysfs writes can still fail (a trigger the kernel does not have),
# and none of them is worth aborting a boot over.
set_trigger() { echo "$1" >"$LED/trigger" 2>/dev/null || return 1; }

# delay_on/delay_off only exist once the timer trigger is selected, so the
# trigger has to be written first and the delays after.
blink() {
  set_trigger timer || return 0
  echo "$1" >"$LED/delay_on" 2>/dev/null || true
  echo "$2" >"$LED/delay_off" 2>/dev/null || true
}

# mmc0 is the stock Raspberry Pi OS trigger: the LED goes back to meaning "card
# activity", which is what someone who knows Pis expects a healthy unit to do.
normal() { set_trigger mmc0 || set_trigger none || true; }

case "$STATE" in
  provisioning) blink 100 100 ;;
  ready)        blink 1000 1000 ;;
  failed)       blink 60 60 ;;
  running)      normal ;;
  off)          set_trigger none || true ;;
esac

exit 0
