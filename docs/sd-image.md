# Build a combiner from a microSD card

Prepare a card at the bench, put it in a Pi, and let it provision itself — no
shell session, no Go toolchain, and (if you stage the tarball) no Internet.
Running the installer by hand instead: [`setup.md`](setup.md).

**Provisioning needs a network with Internet access.** The first boot refreshes
the apt index and installs `conntrack` and `overlayroot` (plus `systemd-resolved`
if the profile configures mgmt DNS) — a few hundred kB, but a working mirror is
required, along with DHCP and DNS on whatever bench network the unit boots on.
That is a bench-time requirement only: once provisioned, a unit never needs the
network again, which is what makes a rack with no Internet workable.

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
| `--user NAME` | login to create (default: `combiner`) |
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

### Serial console

Staging also sets `enable_uart=1` in `config.txt`, so every unit comes up with a
serial console on the GPIO header at **115200 8N1, no flow control**. Raspberry
Pi OS already puts `console=serial0,115200` on the kernel cmdline, but on a Pi 3
the mini-UART is not enabled without that line, so the console silently goes
nowhere.

It is worth wiring up before a unit is racked. A combiner that will not boot, or
that comes up without SSH, is otherwise only diagnosable by pulling its card.

| Pi pin | Signal | → USB-TTL adapter |
| --- | --- | --- |
| 6 | GND | GND |
| 8 | GPIO14 / TXD | **RX** |
| 10 | GPIO15 / RXD | **TX** |

Use a **3.3V** adapter and leave its power lead disconnected — back-powering the
Pi through the header risks corrupting the card. On macOS use the `cu.` device,
not `tty.` (the latter blocks waiting for carrier detect):

```bash
screen /dev/cu.usbserial-XXXX 115200
```

In minicom, set **Hardware Flow Control: No** (`Ctrl-A` `O` → Serial port
setup → `F`). It defaults to Yes, and a three-wire adapter has no RTS/CTS, so
the window stays silent no matter how healthy the UART is.

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

## Re-homing a unit: bench config, then pave it over

Provisioning needs a mirror for apt; a rack does not have one. So provision on a
bench network, then replace the config before the unit is racked:

1. Stage a card with a **bench** config — an untagged `mgmt` VLAN on a LAN with
   DHCP, a gateway and DNS, such as
   [`site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml). Boot it
   at the shop, let apt run, and verify the unit on your own network.
2. When it is ready to rack, overwrite `combiner-site.yaml` on the boot
   partition with the **production** config — re-run `prep-card.sh` with the
   card in a laptop, or edit it over SSH while the unit is still on the bench.
3. Rack it and power on. `combiner-apply` notices the config differs from what
   is live and paves the new one over the old — no Internet, no console, no
   keyboard.

### Re-staging really does re-provision

cloud-init runs `users`, `runcmd` and everything else that provisions a box
exactly **once per instance-id**, and Raspberry Pi Imager pins that id in two
places: `meta-data` on the seed, and `ds=nocloud;i=<id>` on the **kernel command
line**, where it takes precedence. `prep-card.sh` rewrites both, so re-staging a
card genuinely means "provision this again". Changing only `meta-data` is
silently ineffective — the card looks freshly staged and provisions nothing.

`combiner-apply` runs on every boot from `combiner-apply.service` and compares
the config it would generate against what is actually live. When they match it
exits without restarting anything, so a normal boot costs nothing. It reads the
**boot partition** in preference to `/etc/combiner/site.yaml`, which is what
makes the card the source of truth — edit `/etc` directly and the next boot will
put it back.

A rejected config changes nothing at all: the unit keeps running its previous
configuration, and the reason lands in `combiner-apply.log` on the boot
partition. Validate before you commit to a swap:

```bash
./deploy/pi/prep-card.sh --check-card     # from a laptop, card inserted
sudo combiner-apply --dry-run             # on the unit itself
```

Verification has to happen **before** the pave, because afterwards the unit is
on show addressing and unreachable from a bench LAN.

## When it does not come up

Pull the card, put it in a laptop, and open **`combiner-firstboot.log`** on the
boot partition. Every step is logged there, and a failure prints a
`PROVISIONING FAILED` banner naming the cause. Re-staging the card moves that
log aside to `combiner-firstboot.log.prev` rather than deleting it, so the boot
you are debugging survives a re-stage.

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

## Read-only root

A racked combiner is powered off by pulling the rack. Once a unit has been
sealed and cloned, `combiner-finalize` locks its root filesystem read-only, so
there is no in-flight write for a hard cut to corrupt.

```
root:  overlay  lowerdir=/media/root-ro  upperdir=/media/root-rw/overlay
lower: ext4 ro    <- the SD card
upper: tmpfs rw   <- every write, discarded on reboot
```

Writes still succeed — they land in RAM — so nothing breaks. `combiner-apply`
regenerates the ruleset, networkd units and hostname from
`/boot/firmware/combiner-site.yaml` on **every** boot, which is what makes an
ephemeral `/etc` workable: the card is the source of truth and the running
config is rebuilt from it each time. Changing a racked unit is still
pull-card → edit YAML → reseat.

### Why it locks on the second boot, not at seal time

A sealed card has no identity: `combiner-seal` clears machine-id and the SSH
host keys so each clone generates its own. Under a read-only root those writes
would land in tmpfs and be discarded, and the unit would present a **different
host key on every reboot**. So the first boot of a clone runs writable;
`combiner-finalize` waits until the identity exists and the config has applied,
then locks the root and reboots.

It refuses to lock, and retries on the next boot, when:

- machine-id is still empty or there are no host keys — the clone has not
  finished becoming itself
- `combiner-apply` has not succeeded — freezing a misconfigured unit only makes
  it harder to fix
- `overlayroot` is not installed — it ships as a provisioning dependency
  precisely so locking needs no network

### Unlocking for maintenance

Locking is one token on the kernel command line, so it is reversible from a
laptop with nothing but the card:

```bash
sudo raspi-config nonint disable_overlayfs && sudo reboot   # on the unit
```

or delete `overlayroot=tmpfs` from `cmdline.txt` on the boot partition. The
card's own `/etc/fstab` is never modified — overlayroot rewrites it only in the
tmpfs layer — so unlocking restores ordinary behaviour with nothing to undo.

### What else the appliance does about unclean power

| | |
| --- | --- |
| Root read-only | The card is never written during normal operation |
| journald `Storage=volatile`, capped at 32M | Logs live in RAM. Set at lock time, not install time, so a bench unit keeps a persistent journal to debug with |
| No swap | The swapfile is removed at install: SD wear, and a corruption risk on a hard cut, for something a combiner never needs |
| Hardware watchdog | Already enabled by Raspberry Pi OS itself (`RuntimeWatchdogSec=1m`); a hung kernel reboots without anyone driving to the rack |
| `fsck.repair=yes`, `noatime` | Already in the stock Raspberry Pi OS cmdline and fstab |

`systemd-remount-fs` is skipped while the overlay is active — it tries to
remount `/` from fstab, which overlayfs rejects outright, and a permanently
failed unit would destroy `systemctl --failed` as a health signal on a box
nobody can inspect. The condition lifts by itself when the root is unlocked.

## Changing the config on a locked unit

Locking the root does **not** freeze the configuration. `combiner-site.yaml`
lives on the FAT boot partition, which stays writable, and `combiner-apply`
re-reads it on every boot. So the field workflow is unchanged by locking:

1. Pull the card, edit `combiner-site.yaml` on a laptop (it mounts in Finder and
   Explorer), reseat it.
2. Power on. `combiner-apply` notices the config differs from what is live and
   paves the new one in.

`prep-card.sh --check-card` validates the edited file before you boot it, which
is worth doing — a rejected config leaves the unit running its previous one and
the reason lands in `combiner-apply.log` on the card.

The only thing locking prevents is a change made *inside* the running system
persisting: edit `/etc/combiner/site.yaml` directly and the next boot puts it
back from the card. That is deliberate — the card is the source of truth.

## Updating a racked unit## Updating a racked unit

With a read-only root and no uplink, the honest answer is usually **swap the
card** — cards are cheap, the config lives on the card's FAT partition, and a
spare can be prepared at the bench and verified before anyone touches the rack.
A binaries-only `combiner-update` over SSH stays useful for units that are
reachable, gated on `combiner -check` the way `install.sh` already is.
