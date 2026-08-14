# Prepare a Raspberry Pi for the combiner

Lab bring-up for Debian / Raspberry Pi OS before running [`deploy/pi/install.sh`](../deploy/pi/install.sh).

**Lab board:** Raspberry Pi **3** `virgil01` at `192.168.1.2`, running **64-bit** Raspberry Pi OS (`uname -m` → `aarch64`). Use **`make build-pi`** (`linux/arm64`). Prefer Pi **4/5** later for trunked / GbE work; Pi 3 is 100 Mbps Ethernet and short on RAM for on-device `go build`.

## Two ways to get binaries onto the Pi

| Approach | When to use | Go on the Pi? |
| --- | --- | --- |
| **A. Cross-compile on a laptop** (recommended) | Normal path; Pi 3 has little RAM for `go build` | **No** |
| **B. Build on the Pi** | Convenience if you already develop on-device | **Yes** |

Runtime packages (nftables, dnsmasq, etc.) are required either way. The installer installs most of them; this doc lists what to have ready and how to build.

---

## Architecture (arm vs arm64)

On the Pi:

```bash
uname -m
dpkg --print-architecture
getconf LONG_BIT
```

| Result | Typical Pi OS | Cross-compile |
| --- | --- | --- |
| `aarch64` / `arm64`, 64-bit | 64-bit Raspberry Pi OS (**lab: `virgil01`**) | `make build-pi` → `GOOS=linux GOARCH=arm64` |
| `armv7l` / `armhf`, 32-bit | Older 32-bit Pi OS images | `make build-pi-arm` → `GOOS=linux GOARCH=arm GOARM=7` |

Match the binary to the OS. A `linux/arm` binary will not run on `aarch64` Pi OS (and vice versa).

---

## Packages

### Always (runtime / install)

`install.sh` installs these if missing; you can pre-install:

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
# Lab default: aarch64 (virgil01 and any 64-bit Pi OS)
make build-pi
# → bin/combiner-linux-arm64 , bin/combiner-status-linux-arm64

# Only if uname -m is armv7l / armhf
make build-pi-arm
# → bin/combiner-linux-arm , bin/combiner-status-linux-arm
```

Equivalent manual command for the lab Pi:

```bash
GOOS=linux GOARCH=arm64 go build -o bin/combiner-linux-arm64 ./cmd/combiner
GOOS=linux GOARCH=arm64 go build -o bin/combiner-status-linux-arm64 ./cmd/combiner-status
```

Copy to `virgil01`. `install.sh` expects plain names `bin/combiner` and `bin/combiner-status` when `go` is not on the Pi:

```bash
# From laptop
scp bin/combiner-linux-arm64 mpsllc@192.168.1.2:~/combiner
scp bin/combiner-status-linux-arm64 mpsllc@192.168.1.2:~/combiner-status

# On virgil01
mkdir -p ~/vunet-dante-combiner-2000/bin
mv ~/combiner ~/combiner-status ~/vunet-dante-combiner-2000/bin/
```

---

## Get the repo onto the Pi

```bash
# On the Pi
cd ~
git clone https://github.com/misnow1/vunet-dante-combiner-2000.git
cd vunet-dante-combiner-2000
```

Or rsync/scp from your laptop. Then either build on-device or drop prebuilt binaries into `bin/`.

---

## Minimal lab config (no production trunk yet)

For first smoke tests on a single LAN (`virgil01` at `192.168.1.2`), you still need three **VLAN IDs** on a trunk to exercise the full installer. Until the switch is ready:

1. Cross-compile / copy binaries and run `combiner -check -config config/site.example.yaml`
2. Generate nftables only: `python3 deploy/pi/generate-nftables.py config/site.example.yaml /tmp/nft.conf && sudo nft -c -f /tmp/nft.conf`
3. Do **not** run full `install.sh` over SSH without `--i-have-console` — it rewrites networking

Edit `config/site.example.yaml` → `/etc/combiner/site.yaml` with real VLAN IDs and addresses before a full install. On a shared lab LAN that already runs DHCP, set `mgmt_dhcp.enabled: false` so `install.sh` does not start dnsmasq. For native/untagged Mgmt on the Pi NIC, set `vlans.mgmt.untagged: true`. Production-shaped defaults are documented in [`packet-flow.md`](packet-flow.md).

---

## Checklist before `install.sh`

- [ ] OS architecture known; binaries match (`arm64` for `virgil01` / `aarch64`)
- [ ] `python3` + `python3-yaml` available
- [ ] `nft` available (`nftables` package)
- [ ] Repo present; `bin/combiner` and `bin/combiner-status` exist **or** `go` can build them
- [ ] `/etc/combiner/site.yaml` edited for this site
- [ ] Local console available (or you accept `--i-have-console` over SSH knowing you may lock yourself out)
- [ ] Switch trunk prepared for Mgmt + Control + Dante tags

Then continue with [`../deploy/pi/README.md`](../deploy/pi/README.md).
