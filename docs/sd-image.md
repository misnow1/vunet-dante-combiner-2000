# Build a combiner from a microSD card

Prepare a card at the bench, put it in a Pi, and let it provision itself — no
shell session, no Go toolchain, and (if you stage the tarball) no Internet.
Running the installer by hand instead: [`setup.md`](setup.md).

**Provisioning needs a network with Internet access, and does not care which
one.** The first boot refreshes the apt index and installs `conntrack` and
`overlayroot` (plus `systemd-resolved` if the profile configures mgmt DNS) — a
few hundred kB, but a working mirror is required, along with DHCP and DNS. Any
LAN that has those will do; it does not have to resemble the show network.
That is a bench-time requirement only: once provisioned, a unit never needs the
network again, which is what makes a rack with no Internet workable.

**A unit boots twice.** The first boot provisions on DHCP and then *holds*: it
applies nothing, and stays reachable where it is, so it can be verified. The
config on the card lands when an operator runs `combiner-go-live` on the unit,
which reboots it onto its own addressing. See [§3](#3-boot-it).

Three facts about the deployment drive the design:

- **A racked Pi has no Internet.** Anything needing a package mirror happens at
  the bench, before the unit goes in.
- **A configured Pi is unreachable from the bench.** It answers on show VLANs
  and show addresses. So everything that needs checking has to be checked
  *before* the config is applied — hence the hold.
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

Stage the **production** config — the one the unit will actually run in the
rack. There is no separate bench profile to get through the first boot with,
because the first boot does not apply the config at all.

It finds the mounted boot partition, **validates `my-venue.yaml` with
`combiner -check` before writing anything**, substitutes your public key into
the cloud-init template, and stages the release tarballs so the first boot needs
no network. A bad VLAN id or a misspelled key is caught here, at a desk, rather
than on a dark unit in a rack.

It also writes an empty **`combiner-provisioning`** marker onto the card. That
file is the hold: while it exists `combiner-apply` refuses to touch the network,
and `combiner-go-live` removes it. It is a plain empty file on a FAT partition
on purpose — you can see the state, and clear it, with the card in a laptop.

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

You do not have to create the `combiner-provisioning` marker by hand:
`combiner-firstboot.sh` creates it if it is missing, precisely because this path
has no `prep-card.sh` run behind it. A hand-staged card holds like any other.

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
whole reason the combiner SNATs. A Pi that needs its own uplink once it is live
gets it from an untagged `mgmt` VLAN, which is what
[`site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml) is for.
That profile is a flat-LAN lab config, not a step in provisioning — nothing
needs it to get a card through its first boot any more.

## 3. Boot it

Eject, insert, and power on **on any LAN with DHCP and Internet**. It does not
have to be the show network, and the config on the card is not applied yet.

On the first boot the Pi:

1. installs the runtime packages over the DHCP address it came up with,
2. verifies and unpacks the release tarball (staged one preferred, else
   downloaded) into `/opt/combiner`,
3. copies `combiner-site.yaml` to `/etc/combiner/site.yaml`,
4. runs the normal [`install.sh`](../deploy/pi/install.sh) with
   `--defer-activation` — the same tested path a manual install uses, minus the
   two steps that would take the unit off this network,
5. records `/var/lib/combiner/provisioned` so later boots skip all of this,
6. **holds.** The activity LED settles into a slow steady blink, and the unit
   stays exactly where it is: same address, same SSH, nothing applied.

Provisioning takes a few minutes. What the LED is saying meanwhile:

| Activity LED | Meaning |
| --- | --- |
| stock card-activity flicker | packages installing — cloud-init's phase, before anything of ours runs |
| fast heartbeat | the combiner is installing itself |
| slow steady blink | provisioned, holding, waiting for you |
| rapid burst | provisioning failed, or `combiner` is not running |
| stock card-activity flicker | live and running normally |

The two flickers are the same signal, which is the honest description: the LED
helper ships in the release tarball, so nothing can drive it until that tarball
is unpacked. Before then the stock trigger is what you get, and apt hammering
the card makes it obvious enough that work is happening.

## 4. Verify it, then go live

The unit is reachable right now and will not be afterwards, so this is the only
convenient moment to check anything. It answers to **`combiner.local`** while it
holds — avahi stays up until go-live, precisely because a held unit is on a DHCP
address nobody chose — and `combiner-firstboot.log` on the card records the
address too. SSH in, and the login prints a reminder of which state it is in:

```bash
sudo combiner-go-live --status     # held or live, and what it will become
sudo combiner-apply --dry-run      # runs the real generators and nft -c
```

`--dry-run` is the useful one: it renders the ruleset and the networkd units
from the card's config and validates them, changing nothing. When you are
satisfied:

```bash
sudo combiner-go-live
```

It re-runs that validation, tells you what the unit is about to become, and asks
once. Then it hands the interfaces from NetworkManager to `systemd-networkd`,
enables the runtime units, clears the hold, and reboots. On the way up
`combiner-apply` paves in the card's config, and the unit answers on its
production addressing — not on the network you were just talking to it over.

Check it the usual way ([`setup.md`](setup.md) §5): `combiner-status` on the
box, or `http://<control-ip>:8080/`.

If a config would be rejected, `combiner-go-live` refuses and changes nothing.
The unit stays on the bench network, which is the entire reason the hold exists.

### Putting a live unit back on a bench

```bash
sudo combiner-go-live --undo
```

It hands the interfaces back to NetworkManager, removes the combiner networkd
units, disables `nftables`, `combiner` and `combiner-apply`, turns forwarding
off, re-creates the hold, and reboots onto DHCP. The config stays on the card,
so `combiner-go-live` puts it back.

**Creating the marker by hand is not enough on a unit that has already gone
live.** The hold stops `combiner-apply` from applying a config; it does not
unpick one that is already applied. The networkd units, ruleset and hostname
would all still be in `/etc` and NetworkManager would still be disabled, so the
unit would come back on show addressing — held, and just as unreachable. On a
card that has never booted, an empty `combiner-provisioning` file is all it
takes.

## Re-homing a unit for a different venue

Locking, sealing and the hold all leave this unchanged: **the card is the source
of truth**, and `combiner-apply` re-reads it on every boot.

1. Pull the card, overwrite `combiner-site.yaml` with the new config, reseat it.
   `prep-card.sh --check-card` validates the edit before you boot it.
2. Power on. `combiner-apply` notices the config differs from what is live and
   paves the new one over the old — no Internet, no console, no keyboard.

Editing it over SSH on the unit works too, if you can still reach it.

Re-running the full `prep-card.sh` (rather than editing the file in place)
re-arms the hold, so the unit waits for another `combiner-go-live` before it
applies anything. It does **not** re-provision: `/var/lib/combiner/provisioned`
lives on the root filesystem, which `prep-card.sh` never touches, so the next
boot reuses the software already installed. That is usually what you want when
a card changes hands — new config, new verification, same known-good build.

It also means **`--version` is not an upgrade lever** on a card whose root has
already provisioned. To move a unit to a new release, re-flash the card (see
[Updating a racked unit](#updating-a-racked-unit)).

### What re-staging does and does not redo

cloud-init runs `users`, `runcmd` and everything else that provisions a box
exactly **once per instance-id**, and Raspberry Pi Imager pins that id in two
places: `meta-data` on the seed, and `ds=nocloud;i=<id>` on the **kernel command
line**, where it takes precedence. `prep-card.sh` rewrites both, so re-staging a
card genuinely re-runs cloud-init. Changing only `meta-data` is silently
ineffective — the card looks freshly staged and cloud-init skips it entirely.

What cloud-init then re-runs is `combiner-firstboot.sh`, and that stops at
`/var/lib/combiner/provisioned` on an already-provisioned root. So re-staging
gives you a fresh config and a fresh hold, not a fresh install.

Re-staging also re-writes the `combiner-provisioning` marker, so a re-staged
card holds on its next boot even if that unit had already gone live once. A card
that has been re-staged has, by definition, not been verified since.

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

**Try SSH first.** A unit whose provisioning failed never left the network it
booted on — the hold is written onto the card at staging time, before anything
can go wrong — so it should still answer at its DHCP address, with the failure
in `journalctl` and the LED in a rapid burst. That is usually quicker than
pulling the card, and it is the main thing the two-phase boot buys you.

Failing that: pull the card, put it in a laptop, and open
**`combiner-firstboot.log`** on the boot partition. Every step is logged there,
and a failure prints a `PROVISIONING FAILED` banner naming the cause.
Re-staging the card moves that log aside to `combiner-firstboot.log.prev`
rather than deleting it, so the boot you are debugging survives a re-stage.
`combiner-golive.log` and `combiner-apply.log` are next to it and cover the two
later steps.

A failed provision leaves IP forwarding **off**. That is the safe state: the
unit will not bridge Control and Dante until the problem is fixed. Re-staging
the card and rebooting is the normal fix; the `provisioned` marker lives on the
root filesystem, so a re-flashed card always re-provisions.

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

A racked combiner is powered off by pulling the rack, so its root filesystem is
locked read-only before it goes in: there is no in-flight write for a hard cut
to corrupt. `combiner-finalize` does the locking, at boot, and something has to
arm it first.

For **a single unit going into a rack as itself**, that is `combiner-lock`:

```bash
sudo combiner-lock            # arm; the next boot locks and reboots once more
sudo combiner-lock --status   # root now, next boot, armed?, overlayroot present?
sudo combiner-lock --off      # release it; writable again after the next boot
```

Arming preflights at the bench, where you are watching: it refuses a unit with
no machine-id or host keys of its own, or whose `/etc/combiner/site.yaml` does
not validate. `--status` reports "root now" and "next boot" separately, because
after either operation they differ for a whole boot.

For **a card that will be cloned onto spares**, `combiner-seal` arms the same
mechanism as part of stripping the unit's identity — see
[`deploy/pi/README.md`](../deploy/pi/README.md).

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
- the unit is still holding — it has never applied a config at all, and a held
  unit's `combiner-apply` succeeds *by doing nothing*, so this has to be checked
  separately from the one below
- `combiner-apply` has not succeeded — freezing a misconfigured unit only makes
  it harder to fix
- `overlayroot` is not installed — it ships as a provisioning dependency
  precisely so locking needs no network

### Unlocking for maintenance

The short version is `sudo combiner-lock --off && sudo reboot`. The longer
routes below still work and are worth knowing when the unit will not boot.

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

## Updating a racked unit

With a read-only root and no uplink, the honest answer is usually **swap the
card** — cards are cheap, the config lives on the card's FAT partition, and a
spare can be prepared at the bench and verified before anyone touches the rack.
A binaries-only `combiner-update` over SSH stays useful for units that are
reachable, gated on `combiner -check` the way `install.sh` already is.
