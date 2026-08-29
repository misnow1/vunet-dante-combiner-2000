# Build binaries and lab boards

If you are installing a combiner on a show network, start at **[`setup.md`](setup.md)** (addresses, switch, DHCP, release tarball, `install.sh`). This page is for **developers**: matching `arm` vs `arm64`, Go, cross-compile, and the `virgil` lab Pi.

**Lab board:** Raspberry Pi **3** `virgil` at `192.168.33.212`, **64-bit** Raspberry Pi OS (`uname -m` → `aarch64`). Use the **`linux-arm64` release tarball**, or **`make build-pi`**. Prefer Pi **4/5** for production trunks (GbE); Pi 3 is 100 Mbps and short on RAM for on-device `go build`.

## Ways to get binaries onto the Pi

| Approach | When to use | Go on the Pi? |
| --- | --- | --- |
| **C. Download a GitHub release** (recommended) | Lab / field install; no toolchain needed | **No** |
| **A. Cross-compile on a laptop** | Developing before a release exists | **No** |
| **B. Build on the Pi** | Convenience if you already develop on-device | **Yes** |

Runtime packages (nftables, dnsmasq, etc.) are required either way. The installer
probes for them and only calls `apt` for what is genuinely missing, so a Pi that
was prepared at the bench installs fine with no Internet. This doc lists what to
have ready and how to get binaries.

---

## Architecture (arm64 / arm / amd64)

On the Pi:

```bash
uname -m
dpkg --print-architecture
getconf LONG_BIT
```

| Result | Typical OS | Release asset / cross-compile |
| --- | --- | --- |
| `aarch64` / `arm64`, 64-bit | 64-bit Raspberry Pi OS (**lab: `virgil`**) | `*-linux-arm64.tar.gz` / `make build-pi` |
| `armv7l` / `armhf`, 32-bit | Older 32-bit Pi OS images | `*-linux-arm.tar.gz` / `make build-pi-arm` |
| `x86_64` / `amd64` | Debian/Ubuntu x86_64 | `*-linux-amd64.tar.gz` / `make build-linux-amd64` |

Match the binary to the OS. A `linux/arm` binary will not run on `aarch64` Pi OS (and vice versa).

---

## Download a release (approach C)

1. Open [Releases](https://github.com/misnow1/vunet-dante-combiner-2000/releases) and download the tarball for your arch (plus `SHA256SUMS` if you want to verify).
2. On the Pi:

```bash
# Example for virgil / aarch64 — replace VERSION (no leading v)
curl -fsSL -o combiner.tgz \
  https://github.com/misnow1/vunet-dante-combiner-2000/releases/download/vVERSION/vunet-dante-combiner-VERSION-linux-arm64.tar.gz
tar -xzf combiner.tgz
cd vunet-dante-combiner-VERSION-linux-arm64
# optional: sha256sum -c SHA256SUMS  (if you also downloaded SHA256SUMS into this directory's parent)
```

The tree already has `bin/combiner`, `bin/combiner-status`, `config/`, and `deploy/pi/`. Field install continues in [`setup.md`](setup.md). Installer troubleshooting: [`../deploy/pi/README.md`](../deploy/pi/README.md).

Maintainers publish packages by pushing a tag: `git tag v0.2.4 && git push origin v0.2.4`.

---

## Packages

### Always (runtime / install)

`install.sh` installs these if missing; pre-installing them means the install
never touches the network — which is what you want for anything going into a
rack (see [no Internet on the Pi](#no-internet-on-the-pi)):

```bash
sudo apt-get update
sudo apt-get install -y \
  dnsmasq \
  nftables \
  python3 \
  python3-yaml \
  iproute2 \
  conntrack \
  git \
  curl \
  ca-certificates
```

Optional but useful for debugging:

```bash
sudo apt-get install -y tcpdump tshark iperf3
```

### No Internet on the Pi

A racked combiner has no uplink, so `install.sh` never calls `apt` speculatively:

- **Nothing missing** — it prints `all runtime dependencies present — skipping
  apt` and does not touch the network at all.
- **Something missing, apt reachable** — it installs just those packages, with a
  hard timeout and no retries so a dead mirror fails fast instead of hanging.
- **Something missing, no mirror** — it names the packages and exits **before
  changing anything**.

For that last case, stage the `.deb` files on a USB stick and point the installer
at them:

```bash
# On a Pi of the SAME architecture that does have Internet:
mkdir -p ~/combiner-debs && cd ~/combiner-debs
apt-get download nftables python3-yaml iproute2 conntrack   # + dnsmasq if mgmt_dhcp

# On the racked Pi, with the stick mounted:
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --offline-debs /media/usb/combiner-debs
```

`apt-get download` fetches only the named packages, not their dependencies — on a
stock Raspberry Pi OS Lite install the rest are already present, but check the
installer output rather than assuming.

Flashing a card that arrives pre-provisioned is the better long-term answer:
[`sd-image.md`](sd-image.md).

Kernel VLAN module (usually present):

```bash
sudo modprobe 8021q
lsmod | grep 8021q
```

### Only if building on the Pi (approach B)

Prefer a current Go toolchain matching `go.mod` (this repo uses **Go 1.25+**). Raspberry Pi OS `golang-go` is often too old.

**Recommended on-device install** (official tarball from https://go.dev/dl/):

```bash
# Example for 64-bit Pi OS — pick the matching .tar.gz for your arch/version
curl -fsSL -o /tmp/go.tgz https://go.dev/dl/go1.25.0.linux-arm64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tgz
```

For **32-bit** Pi OS use e.g. `go1.25.0.linux-armv6l.tar.gz` (works on armv7 as well).

Verify:

```bash
go version
```

---

## Environment variables

### Shell profile (build on Pi or cross-compile host)

Add to `~/.profile` or `~/.bashrc` if Go was installed under `/usr/local/go`:

```bash
export PATH="/usr/local/go/bin:$PATH"
# Optional; modules mode is default for modern Go:
# export GO111MODULE=on
```

Reload:

```bash
source ~/.profile
```

**Not required for the combiner at runtime.** The service is started by systemd with:

```text
/usr/local/bin/combiner -config /etc/combiner/site.yaml
```

No `COMBINER_*` environment variables are used today.

### Cross-compile from macOS/Linux laptop (approach A)

From the repo root:

```bash
# Lab default: aarch64 (virgil and any 64-bit Pi OS)
make build-pi
# → bin/combiner-linux-arm64 , bin/combiner-status-linux-arm64

# Only if uname -m is armv7l / armhf
make build-pi-arm
# → bin/combiner-linux-arm , bin/combiner-status-linux-arm

# x86_64 Linux hosts
make build-linux-amd64

# Or build all release tarballs locally
make package VERSION=0.0.0-dev
```

Copy to `virgil`. `install.sh` expects plain names `bin/combiner` and `bin/combiner-status` (release packages already use those names):

```bash
# From laptop
scp bin/combiner-linux-arm64 combiner@192.168.33.212:~/combiner
scp bin/combiner-status-linux-arm64 combiner@192.168.33.212:~/combiner-status

# On virgil
mkdir -p ~/vunet-dante-combiner-2000/bin
mv ~/combiner ~/combiner-status ~/vunet-dante-combiner-2000/bin/
```

---

## Get sources onto the Pi (approaches A / B)

Prefer a [release tarball](#download-a-release-approach-c) for installs. For development:

```bash
# On the Pi
cd ~
git clone https://github.com/misnow1/vunet-dante-combiner-2000.git
cd vunet-dante-combiner-2000
```

Or rsync/scp from your laptop. Then either build on-device or drop prebuilt binaries into `bin/`. `install.sh` prefers existing `bin/` executables and only runs `go build` if they are missing.

---

## Minimal lab config (no production trunk yet)

For first smoke tests on a single LAN (`virgil` at `192.168.33.212`), use [`config/site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml) (optional untagged Mgmt). Production is [`config/site.example.yaml`](../config/site.example.yaml) (audio trunk: clients on untagged Dante, amps on tagged Control); if the port has no untagged VLAN at all, make the one-line change its `dante:` block documents. Until the switch is ready:

1. Use a release tree or cross-compiled binaries and run `./bin/combiner -check -config config/site.example.yaml`
2. Generate nftables only: `python3 deploy/pi/generate-nftables.py config/site.example.yaml /tmp/nft.conf && sudo nft -c -f /tmp/nft.conf`
3. Do **not** run full `install.sh` over SSH without `--i-have-console` — it rewrites networking

Edit `site.yaml` with real VLAN IDs and addresses before a full install. Combiner DHCP is off unless you explicitly enable it on an optional Mgmt VLAN. Switch, DHCP, and the canonical install steps: [`setup.md`](setup.md).

---

## Checklist before `install.sh`

- [ ] OS architecture known; binaries match (`arm64` for `virgil` / `aarch64`)
- [ ] `python3` + `python3-yaml` available
- [ ] `nft` available (`nftables` package)
- [ ] Release tree **or** repo with `bin/combiner` + `bin/combiner-status` (or `go` to build them)
- [ ] If the Pi has no Internet: runtime packages already installed, or `.deb` files staged for `--offline-debs`
- [ ] `/etc/combiner/site.yaml` edited for this site
- [ ] Local console available (or you accept `--i-have-console` over SSH knowing you may lock yourself out)
- [ ] Switch + DHCP prepared per [`setup.md`](setup.md)

Then continue with [`setup.md`](setup.md) or [`../deploy/pi/README.md`](../deploy/pi/README.md) if the installer fails.
