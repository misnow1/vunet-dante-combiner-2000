# Raspberry Pi installer

What `install.sh` does and how to recover when it fails. **Addresses, switch ports, DHCP, and the install commands to run:** **[`docs/setup.md`](../../docs/setup.md)**. Building binaries / Go / `virgil01`: [`docs/pi-prep.md`](../../docs/pi-prep.md).

Installs VLAN interfaces, optional lab Mgmt DHCP (`dnsmasq`), fail-closed `nftables`, and the `combiner` service on Debian / Raspberry Pi OS.

`install.sh` is also what a self-provisioning card runs on first boot — see
[`docs/sd-image.md`](../../docs/sd-image.md) and `cloud-init/` in this directory.

## Order of operations

Everything that can reject the install happens **before** anything on the box
changes, so a failed run leaves no residue:

1. Argument, root, and SSH checks.
2. Resolve `bin/combiner` + `bin/combiner-status` (build them only if this is a
   source checkout with Go available).
3. Stage `site.yaml` and the allowlists in a temp dir and run `combiner -check`
   against that staged pair — the same layout the install will produce, so
   `allowlist_files` resolve exactly as they will in `/etc/combiner`.
4. Read `site.yaml` facts via `combiner -print-facts`. The installer does not
   parse YAML with Python, because it is the thing responsible for installing
   `python3-yaml`.
5. Resolve runtime packages: skip `apt` entirely if nothing is missing, else
   install from `--offline-debs` or `apt`, else abort with the exact list.

Only after all five does it load the module, disable `avahi`, write
`/etc/combiner`, and rewrite networking.

## Prerequisites

- Raspberry Pi with GbE (Pi 4/5 recommended; Pi 3 OK for early lab)
- Switch trunk and DHCP already set per **[`docs/setup.md`](../../docs/setup.md)**
- **Local console** (serial/HDMI) recommended — install disables NetworkManager/dhcpcd
- Edited `/etc/combiner/site.yaml` (start from `config/site.example.yaml` — audio trunk; or `site.tagged-trunk.example.yaml` / `site.lab-flat.example.yaml`)

### Combiner DHCP (off by default)

Production Control DHCP stays on the core switch. Set `mgmt_dhcp.enabled: true` only when an optional **Mgmt** VLAN exists and you want the combiner to serve it.

```yaml
mgmt_dhcp:
  enabled: false
```

### Untagged VLANs (native / PVID)

Any **one** VLAN may be untagged, because a switch port has exactly one PVID. Its L3 lands on `physical_interface` and no `.netdev` is written for it; every other VLAN stays an 802.1Q subinterface. The generators reject a second `untagged: true` with `only one VLAN may be untagged (a port has one PVID)`.

**Audio trunk (default)** — PVID/untagged Dante, tagged Control, from [`config/site.example.yaml`](../../config/site.example.yaml):

```yaml
vlans:
  control:
    id: 200
    address: 10.200.0.1
    prefix: 21
    interface_name: eth0.200   # tagged
  dante:
    id: 201                    # documents the PVID; no eth0.201 is created
    address: 10.201.0.1
    prefix: 21
    untagged: true             # L3 lands on eth0
```

Result: `10-combiner-trunk.network` carries `Address=10.201.0.1/21` plus `VLAN=eth0.200`, and only `20-combiner-control.netdev` is written.

**Untagged Mgmt (flat lab LAN)** — lab boards on an access/native port (e.g. `virgil01` on `192.168.1.x`) use [`config/site.lab-flat.example.yaml`](../../config/site.lab-flat.example.yaml). That Mgmt face is a **Pi uplink**, not the client network. Production clients live on Control.

```yaml
vlans:
  mgmt:
    id: 1                    # switch PVID / native VLAN id (docs + uniqueness)
    address: 192.168.1.2
    prefix: 24
    untagged: true
    gateway: 192.168.1.1     # lab only — see below
    dns: [192.168.1.1]
  control: { ... }           # still tagged eth0.<id>
  dante: { ... }
```

Mgmt L3 lands on `physical_interface` (no `eth0.<mgmt-id>`); Control and Dante stay 802.1Q subinterfaces. Switch port: untagged Mgmt + tagged Control/Dante, or a temporary access port for Mgmt-only smoke tests.

**`gateway` / `dns` are lab-only** for the Pi’s own uplink. They are never advertised to Control clients.

Reserve the Mgmt address in the LAN router so nothing else claims it.

## Install

**From a GitHub release** (preferred — no Go on the Pi):

```bash
# On the Pi — pick arm64 / arm / amd64 to match uname -m; replace VERSION (no leading v)
curl -fsSL -o combiner.tgz \
  https://github.com/misnow1/vunet-dante-combiner-2000/releases/download/vVERSION/vunet-dante-combiner-VERSION-linux-arm64.tar.gz
tar -xzf combiner.tgz
cd vunet-dante-combiner-VERSION-linux-arm64

sudo mkdir -p /etc/combiner
sudo cp config/site.example.yaml /etc/combiner/site.yaml   # or site.tagged-trunk / site.lab-flat
# edit /etc/combiner/site.yaml
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

### Options

```text
usage: install.sh [SITE_YAML] [--i-have-console] [--offline-debs DIR]

  SITE_YAML           path to site.yaml (default /etc/combiner/site.yaml)
  --i-have-console    proceed over SSH, accepting that this may lock you out
  --offline-debs DIR  install runtime packages from .deb files in DIR instead
                      of apt (for racked units with no Internet)
```

`install.sh` uses the prebuilt `bin/` binaries and does not require Go.

**From a git checkout** (dev / cross-compile):

```bash
sudo mkdir -p /etc/combiner
sudo cp config/site.example.yaml /etc/combiner/site.yaml   # or site.tagged-trunk / site.lab-flat
sudo cp -r config/allowlists /etc/combiner/
# edit /etc/combiner/site.yaml
# ensure bin/combiner and bin/combiner-status exist (release package, make build-pi, or go on PATH)
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

Without `--i-have-console`, the script refuses to run over SSH (it would lock you out).

### What the installer does (order matters)

1. Resolves `bin/combiner` and `bin/combiner-status` **before touching anything** — rebuilding them when a source checkout has Go files newer than the binary, and aborting with `nothing has been changed on this system` if they are missing and Go is unavailable
2. Runs `combiner -check` on `site.yaml` as an authoritative preflight, and leaves **IP forwarding off**
3. Generates nftables and runs **`nft -c -f`** (abort if invalid)
4. Loads a bootstrap **forward drop** ruleset, then the real ruleset
5. Flushes conntrack
6. Configures VLANs / dnsmasq / combiner
7. **Waits for every interface named in `site.yaml` to exist** — aborts with diagnostics if not
8. **Enables IP forwarding last**
9. Confirms `combiner` is still active a few seconds after start

Avahi is disabled/masked so it does not fight the reflector on UDP 5353. The install aborts if `NetworkManager` or `dhcpcd` survive the disable step, since either one silently prevents `systemd-networkd` from creating VLANs.

## Troubleshooting

**No VLAN interfaces after install (`ip -br addr` shows only `eth0`)**

```bash
systemctl is-active systemd-networkd NetworkManager dhcpcd
networkctl list
journalctl -u systemd-networkd -b --no-pager | tail -40
lsmod | grep 8021q
ls /etc/systemd/network/
```

Read the networkd journal first — it names the failing line and file. Causes seen so far:

1. **`Invalid netdev name in VLAN=`** — `VLAN=` accepts one netdev per assignment, so the generator writes a separate `VLAN=` line per interface. A space-separated list makes networkd discard the assignment and create nothing.
2. **`Could not process new link message for netdev` on the physical NIC, link shown as `unmanaged`** — a `.netdev` named after the physical interface makes networkd treat the real link as a VLAN it must create. That happens with `interface_name: eth0` on a tagged VLAN (now rejected) or a stale unit from an earlier run. The installer deletes `/etc/systemd/network/*combiner*` before writing; clear leftovers by hand if you generated units another way.
3. **A competing manager still owns the NIC** — `eth0` showing a `dynamic noprefixroute` address means NetworkManager/dhcpcd is still in charge. `sudo systemctl disable --now NetworkManager dhcpcd`, then re-run.
4. **Missing `8021q`** — `sudo modprobe 8021q` must succeed; the installer aborts if it does not.

Per-interface `IPForward=` is intentionally absent from the generated units: it is deprecated in current systemd, and forwarding is owned by `/etc/sysctl.d/99-combiner.conf`, applied only after the ruleset is live.

Control/Dante interfaces existing but staying `no-carrier` is expected until the switch port is a real trunk.

**`combiner` not running**

```bash
systemctl status combiner --no-pager
journalctl -u combiner -b --no-pager | tail -40
combiner -check -config /etc/combiner/site.yaml
```

`-check` prints the interfaces, VLAN tagging, management access, and reflector memberships the service would use, and exits non-zero on a config or allowlist error. Missing interfaces are a warning, not an error, and are the usual reason the service will not stay up.

**Preflight rejects a config that should be valid**

```
config: dante: untagged is only allowed on mgmt
site.yaml failed validation — aborting before any change
```

A rule that no longer exists means the preflight ran a **stale `bin/combiner`** — an older binary validating a newer `site.yaml`. The installer now rebuilds automatically when Go is present and the checkout's `.go` files are newer than the binary; without Go it warns and tells you the rejection may be bogus. Fix by rebuilding (`make build`, or `make build-pi` and copy) or by installing from a release tarball, where the binary and generators are always built together.

**`site.yaml` key was ignored**

It is not. Both the Go loader and the deploy generators reject unrecognized keys and name the offending line, so a typo like `untaged` fails loudly rather than silently selecting the wrong tagging. The Go struct tags in `internal/config` are the authoritative key list; `deploy/pi/site_config.py` mirrors them for the generators.

The service exits when a configured interface is absent, so fix the interfaces first. `status=226/NAMESPACE` instead means a sandbox path in the unit does not exist — the unit reads dnsmasq leases but never writes them, so it declares no `ReadWritePaths` (`ProtectSystem=strict` already leaves the filesystem readable).

**DNS broken on the Pi, `resolvectl` hangs**

```bash
systemctl is-active systemd-resolved
ls -l /etc/resolv.conf
sudo systemctl enable --now systemd-resolved
```

Installing `systemd-resolved` repoints `/etc/resolv.conf` at `/run/systemd/resolve/stub-resolv.conf`, which exists only while resolved runs; otherwise every lookup fails and `resolvectl` blocks on D-Bus activation until it times out. The installer only pulls resolved in when `vlans.mgmt.dns` is set, starts it explicitly, and warns if it is installed but inactive.

## Switch port

Production trunk, WAP, and DHCP: **[`docs/setup.md`](../../docs/setup.md)**. Lab untagged Mgmt is described under [Untagged Mgmt](#untagged-mgmt-native-vlan--flat-lab-lan) above.

## Clients

- Production: Control SSID/access; status at `http://<control-ip>:8080/`
- Combiner DHCP is off unless `vlans.mgmt` exists and `mgmt_dhcp.enabled` is true
- `combiner.local` is not provided unless you add your own mDNS elsewhere

## Verify

```bash
ip -br addr
sudo nft list counters table inet combiner
sudo combiner-status
combiner -check -config /etc/combiner/site.yaml
curl -s http://127.0.0.1:8080/api/status | head
```

Healthy on a live Dante network: `drop_ptp` / `drop_forward_mcast` may be non-zero. That is good.

## Regenerating nftables only

**Editing `/etc/combiner/site.yaml` and restarting `combiner` is not enough.**
The reflector re-reads the YAML on restart, but the firewall and NAT rules are
only rewritten by `install.sh` or by the procedure below. Skipping it leaves the
kernel enforcing rules generated from the *previous* config while `combiner`
reports the new one — in particular a `snat_to_dante` rule pointing at an
address the box no longer holds, which makes every Control→Dante reply
undeliverable and leaves the flows `[UNREPLIED]` in `conntrack -L`.

```bash
sudo ./deploy/pi/generate-nftables.sh /etc/combiner/site.yaml /tmp/nft.conf
sudo nft -c -f /tmp/nft.conf
sudo cp /tmp/nft.conf /etc/nftables.conf
sudo nft -f /etc/nftables.conf
sudo conntrack -F || true
```

`conntrack -F` is required, not optional: NAT is applied to a flow's first
packet, so established entries keep the old mapping until they expire.

### Moving clients to Dante (`client_vlan`)

Dante Controller needs L2 adjacency for the metering tab and device config;
SNAT carries discovery and some control but cannot provide those. When the
operator needs full Dante Controller, put the clients on Dante Primary and let
the combiner carry Martin VU-NET the other way instead — see
[`config/site.dante-client.example.yaml`](../../config/site.dante-client.example.yaml).

`client_vlan` (default `control`) moves three things:

- which VLAN the reflector treats as the client side
- the direction of the unicast forward rule
- the SNAT target (peer devices always see an on-subnet source)

It deliberately does **not** move the PTP/AES67/ATP denies. Those stay anchored
to Control, because they exist to keep the amp stack quiet. If they followed
`client_vlan` the combiner would start dropping PTP toward Dante and break the
clock of the network it is meant to carry — a silent audio failure that looks
nothing like a config error. `deploy/pi/tests/test_generators.py` guards this.

Allowlists name the **peer** role, so a `vlan: control` allowlist is legal only
when `client_vlan: dante`, and vice versa. Reflecting the client VLAN onto
itself is rejected at load.

### Checking for drift

`combiner -check` compares the ruleset actually loaded in the kernel against
`site.yaml` and exits non-zero when they disagree:

```bash
sudo combiner -config /etc/combiner/site.yaml -check
```

```
nftables drift   none (snat_to_dante -> 10.201.0.1, dante iface eth0, control iface eth0.200)
```

A mismatch names the offending rule and prints the regeneration commands. Run it
after any `site.yaml` edit. It needs root — `nft` cannot read the ruleset
otherwise — and is skipped with a note on hosts where `nft` is absent, so it is
safe to run from a laptop.
