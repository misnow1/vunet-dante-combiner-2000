# Pre-configured microSD cards (planned)

How a combiner will be built from a card instead of a shell session. **Nothing on
this page is implemented yet** — it records decisions and the constraints behind
them so the next pass does not have to re-derive them. Installing today:
[`setup.md`](setup.md).

Two facts about the deployment drive every choice here:

- **A racked Pi has no Internet.** Anything that needs a package mirror has to
  happen at the bench, before the unit goes in.
- **A headless appliance is powered off by pulling the rack.** There is no clean
  shutdown, so the filesystem has to tolerate losing power mid-write.

## Why not "build an image and customise it in Imager"

The obvious plan — ship a `.img.xz`, let people flash it with Raspberry Pi Imager
and fill in the hostname and SSH key — does not work:

> Customization of custom image files was never truly supported – it was possible
> only by means of a defect, and that defect was somewhat dangerous.

Imager 2.x assumes `init_format: none` for a local `.img` and offers no
customisation for it. The format is declared in the **OS manifest**, not in the
image, so an image only becomes customisable when it is served from an `os_list`
JSON that names its `init_format`
([formats](https://github.com/raspberrypi/rpi-imager/blob/main/doc/os_customisation_formats.md),
[custom repos](https://www.raspberrypi.com/news/how-to-add-your-own-images-to-imager/)).

So a custom image is not the first step. It is the *last* step, and it needs a
hosted manifest to be worth anything.

## Phase 2 — boot-partition provisioning (no image build)

Raspberry Pi OS (Trixie) ships **cloud-init**, reading `user-data`,
`meta-data`, and `network-config` from the FAT boot partition, and Raspberry Pi
explicitly endorses editing those after flashing
([announcement](https://www.raspberrypi.com/news/cloud-init-on-raspberry-pi-os/)).
That gives a cross-platform prep flow with nothing to build:

1. Flash **stock Raspberry Pi OS Lite** with Imager, using its normal
   customisation for user, SSH key, and locale. Imager 2.0 writes `user-data`.
2. Drop two files onto the same partition — it is FAT32, so it mounts in
   **Finder and Explorer** with no extra tooling:
   - `user-data` — ours, replacing Imager's, with an `# ==== EDIT THESE ====`
     block at the top for the login and SSH key.
   - `combiner-site.yaml` — the site config, same schema as
     [`site.example.yaml`](../config/site.example.yaml).
3. Boot once **at the bench, with Internet**. cloud-init installs the runtime
   packages and the combiner, then runs `install.sh`.
4. Rack it. From here it never needs a mirror again.

Ships as `deploy/pi/cloud-init/`, plus a `prep-card.sh` helper for macOS/Linux
that copies both files onto a mounted `bootfs` and runs `combiner -check` against
the YAML **before** the card ever goes near a Pi. Windows stays a documented
two-file copy.

First boot should prefer a release tarball already sitting on the boot partition
and only fall back to downloading one, so a fully offline bench prep also works.

### Phase 2b — the actual image

Once the contract above is proven, bake it: build `.img.xz` in CI and publish an
`os_list.json` on GitHub Pages so users add one URL in Imager settings and then
pick "VuNET Combiner" with working customisation. The repo is public, so free
`ubuntu-24.04-arm` runners can build natively.

Tooling is still open. [`pi-gen`](https://github.com/RPi-Distro/pi-gen) (via
[`usimd/pi-gen-action`](https://github.com/usimd/pi-gen-action)) is the
well-trodden path; [`rpi-image-gen`](https://github.com/raspberrypi/rpi-image-gen)
is Raspberry Pi's newer appliance-oriented tool, wants a native arm64 Debian
host, and has [known Imager-customisation rough
edges](https://github.com/raspberrypi/rpi-image-gen/issues/182). The image just
bakes in the Phase 2 contract either way, which is why that contract comes first.

## Phase 3 — read-only root

`/boot/firmware/combiner-site.yaml` becomes the single source of truth, with
`/etc/combiner/site.yaml` a symlink to it. A `combiner-apply.service` regenerates
the ruleset and the networkd units into `/run` on every boot and loads them;
root then stays read-only under an overlay permanently.

Two things fall out of that:

- **Nothing persistent to corrupt.** Pulling power cannot damage a filesystem
  that was never mounted writable.
- **A racked unit is reconfigurable without a login.** Pull the card, edit the
  YAML on a laptop, reseat it. That is the only workflow that survives a box with
  no console, no network, and no keyboard.

This is cheaper than it sounds: `generate-nftables.py` and
`generate-network-config.py` already take an arbitrary output directory and write
a self-contained tree, so **neither generator needs to change** — only
`install.sh` splits into a bench-time installer and a boot-time apply.
`combiner.service` already sets `ProtectSystem=strict` and writes nothing.

Supporting hardening, in rough order of value:

| Change | Why |
| --- | --- |
| `overlayroot` package | Read-only root. More reliable than raspi-config's overlay, which has [known Pi 5 / 64-bit issues](https://github.com/raspberrypi/bookworm-feedback/issues/137) |
| journald `Storage=volatile` | Logs to RAM — the largest source of steady SD writes |
| No swap (`dphys-swapfile` off) | Second largest, and pointless on an appliance |
| `noatime`, `fsck.repair=yes` | Fewer writes; automatic repair after a hard cut |
| `/boot/firmware` mounted `ro` | The config partition is only written during a deliberate change |
| Hardware watchdog (`RuntimeWatchdogSec`) | A hung headless box in a rack reboots itself instead of staying dark |

SSH host keys must be generated during first boot, **before** the overlay is
enabled, so they land in the persistent lower layer and the unit's identity is
stable across reboots.

## Updating a racked unit

With a read-only root and no uplink, the honest answer is usually **swap the
card** — cards are cheap, the config lives on the card's FAT partition, and a
spare can be prepared at the bench and verified before anyone touches the rack.
A binaries-only `combiner-update` over SSH stays useful for units that are
reachable, gated on `combiner -check` the way `install.sh` already is.
