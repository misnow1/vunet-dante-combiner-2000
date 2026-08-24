# Build a combiner from a microSD card

Prepare a card at the bench, put it in a Pi, and let it provision itself — no
shell session, no Go toolchain, and (if you stage the tarball) no Internet.
Running the installer by hand instead: [`setup.md`](setup.md).

Two facts about the deployment drive the design:

- **A racked Pi has no Internet.** Anything needing a package mirror happens at
  the bench, before the unit goes in.
- **A headless appliance is powered off by pulling the rack.** Provisioning logs
  to the card's FAT partition, because the recovery move for a unit that did not
  come up is to read it on a laptop — which cannot mount the ext4 root.

## 1. Flash the card

Flash **Raspberry Pi OS Lite (64-bit, Trixie or newer)** with Raspberry Pi
Imager and use its customisation for the locale and, if you like, a user.
Step 2 replaces the login it configures, so only the locale really survives.

Trixie is required: it is the release that ships
[cloud-init](https://www.raspberrypi.com/news/cloud-init-on-raspberry-pi-os/),
which is what runs the provisioning.

## 2. Stage the provisioning files

### macOS and Linux

With the freshly flashed card re-inserted:

```bash
./deploy/pi/prep-card.sh \
  --site my-venue.yaml \
  --ssh-key ~/.ssh/id_ed25519.pub
```

It finds the mounted boot partition, **validates `my-venue.yaml` with
`combiner -check` before writing anything**, substitutes your public key into
the cloud-init template, and stages the release tarballs so the first boot needs
no network. A bad VLAN id or a misspelled key is caught here, at a desk, rather
than on a dark unit in a rack.

| Option | |
| --- | --- |
| `--site FILE` | required — installed on the card as `combiner-site.yaml` |
| `--ssh-key FILE` | public key to authorise |
| `--ask-password` | prompt for a break-glass password, twice, without echoing |
| `--password-file F` | read that password from the first line of `F` |
| `--password-hash H` | use an already-hashed password (or set `COMBINER_PASSWORD`) |
| `--user-data FILE` | use your own edited `user-data` instead of `--ssh-key` |
| `--card DIR` | boot partition, if autodetection picks wrong |
| `--version V` | release to stage (default: the version this tree ships) |
| `--no-tarball` | skip staging; the Pi downloads on first boot (needs Internet) |
| `--check-card` | re-validate the config already on a card and exit, changing nothing |
| `--force` | skip config validation |

### Break-glass password

A laptop at a show may have no SSH key on it. Add a password login as well as
(or instead of) a key:

```bash
./deploy/pi/prep-card.sh --site my-venue.yaml \
  --ssh-key ~/.ssh/id_ed25519.pub --ask-password
```

It prompts twice without echoing, hashes the password with SHA-512 crypt, and
sets `passwd`, `lock_passwd: false`, and `ssh_pwauth: true` for you. For
unattended staging use `--password-file FILE` or the `COMBINER_PASSWORD`
environment variable; to hash elsewhere, pass `--password-hash`.

Two things worth knowing:

- **The hash goes on the card's FAT partition**, which anyone holding the card
  can read and attack offline. Use a password you have not reused, and treat a
  spare card as a credential. Minimum length is 8.
- **SSH stays restricted to the VLANs named by `management_access`** in
  `site.yaml` — Control by default, not Dante. Enabling password auth does not
  widen where SSH is answered.

`prep-card.sh` refuses a hash that is not really a SHA-512 crypt string. That
catches pasting the password itself into `--password-hash`, and it catches
macOS's `crypt(3)`, which does not implement `$6$` and returns a short DES
value instead — a card that would otherwise reach the rack with a break-glass
password that does not work.

### Windows

Copy three files from `deploy/pi/cloud-init/` onto the card's boot partition
(it is FAT32, so it appears as a drive letter in Explorer):

| Copy | As | Then |
| --- | --- | --- |
| `user-data` | `user-data` (replacing Imager's) | edit the `EDIT THIS BLOCK` section — paste your SSH public key over `REPLACE_WITH_YOUR_SSH_PUBLIC_KEY`, and for a break-glass password follow the comment above `ssh_pwauth` |
| `combiner-firstboot.sh` | `combiner-firstboot.sh` | — |
| your site config | `combiner-site.yaml` | edit VLAN ids and addresses |

Edit them in an editor that keeps Unix line endings and does not add `.txt`
(Notepad on Windows 10+ is fine; VS Code or Notepad++ are safer). Leave
`meta-data` as Imager wrote it.

To make the first boot work offline, also copy
`vunet-dante-combiner-<version>-linux-arm64.tar.gz` and `SHA256SUMS` from
[Releases](https://github.com/misnow1/vunet-dante-combiner-2000/releases) onto
the same partition. Otherwise give the Pi Internet for its first boot.

### If you hand-edit the card afterwards

`prep-card.sh` validates the file it copies, not the copy on the card. If you
edit `combiner-site.yaml` on the card afterwards — which is the normal way to
adjust addresses — re-check it before booting:

```bash
./deploy/pi/prep-card.sh --check-card
```

That runs the same `combiner -check` the Pi will run, against the card, and
changes nothing. Without it the first sign of a bad edit is a failed boot.

A common one: `gateway` and `dns` are accepted **only on `mgmt`**, never on
`control` or `dante`. Dante gear is meant to have no gateway — that is the
whole reason the combiner SNATs. A Pi that needs its own uplink on a flat lab
LAN gets it from an untagged `mgmt` VLAN, which is what
[`site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml) is for.

## 3. Boot it

Eject, insert, power on, and wait a few minutes. On first boot the Pi:

1. installs the runtime packages while the bench network is still up,
2. verifies and unpacks the release tarball (staged one preferred, else
   downloaded) into `/opt/combiner`,
3. copies `combiner-site.yaml` to `/etc/combiner/site.yaml`,
4. runs the normal [`install.sh`](../deploy/pi/install.sh) — the same tested
   path a manual install uses,
5. records `/var/lib/combiner/provisioned` so later boots skip all of this.

Then check it the usual way ([`setup.md`](setup.md) §5): `combiner-status` on
the box, or `http://<control-ip>:8080/`.

## When it does not come up

Pull the card, put it in a laptop, and open **`combiner-firstboot.log`** on the
boot partition. Every step is logged there, and a failure prints a
`PROVISIONING FAILED` banner naming the cause.

A failed provision leaves IP forwarding **off**. That is the safe state: the
unit will not bridge Control and Dante until the problem is fixed. Re-staging
the card and rebooting is the normal fix; the marker file lives on the root
filesystem, so a re-flashed card always re-provisions.

## Why there is no downloadable `.img`

The obvious plan — ship a `.img.xz` and let people fill in the hostname and SSH
key in Imager — does not work:

> Customization of custom image files was never truly supported – it was possible
> only by means of a defect, and that defect was somewhat dangerous.

Imager 2.x assumes `init_format: none` for a local `.img` and offers no
customisation for it. The format is declared in the **OS manifest**, not in the
image, so an image only becomes customisable when served from an `os_list` JSON
that names its `init_format`
([formats](https://github.com/raspberrypi/rpi-imager/blob/main/doc/os_customisation_formats.md),
[custom repos](https://www.raspberrypi.com/news/how-to-add-your-own-images-to-imager/)).

So a custom image is not the first step — it is the last, and it needs a hosted
manifest to be worth anything. The flow above gets the same result today with
nothing to build.

## Planned: the actual image

Bake the contract above into an image: build `.img.xz` in CI and publish an
`os_list.json` on GitHub Pages, so users add one URL in Imager settings and then
pick "VuNET Combiner" with working customisation. The repo is public, so free
`ubuntu-24.04-arm` runners can build natively.

Tooling is still open. [`pi-gen`](https://github.com/RPi-Distro/pi-gen) (via
[`usimd/pi-gen-action`](https://github.com/usimd/pi-gen-action)) is the
well-trodden path; [`rpi-image-gen`](https://github.com/raspberrypi/rpi-image-gen)
is Raspberry Pi's newer appliance-oriented tool, wants a native arm64 Debian
host, and has [known Imager-customisation rough
edges](https://github.com/raspberrypi/rpi-image-gen/issues/182).

## Planned: read-only root

`/boot/firmware/combiner-site.yaml` becomes the single source of truth, with
`/etc/combiner/site.yaml` a symlink to it. A `combiner-apply.service`
regenerates the ruleset and networkd units into `/run` on every boot and loads
them; root then stays read-only under an overlay permanently.

Two things fall out of that:

- **Nothing persistent to corrupt.** Pulling power cannot damage a filesystem
  that was never mounted writable.
- **A racked unit is reconfigurable without a login.** Pull the card, edit the
  YAML on a laptop, reseat it — the only workflow that survives a box with no
  console, no network, and no keyboard.

Cheaper than it sounds: `generate-nftables.py` and `generate-network-config.py`
already take an arbitrary output directory and write a self-contained tree, so
**neither generator needs to change** — only `install.sh` splits into a
bench-time installer and a boot-time apply. `combiner.service` already sets
`ProtectSystem=strict` and writes nothing.

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
