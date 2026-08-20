# Raspberry Pi installer

What `install.sh` does and how to recover when it fails. **Addresses, switch ports, DHCP, and the install commands to run:** **[`docs/setup.md`](../../docs/setup.md)**. Building binaries / Go / `virgil01`: [`docs/pi-prep.md`](../../docs/pi-prep.md).

Installs VLAN interfaces, optional lab Mgmt DHCP (`dnsmasq`), fail-closed `nftables`, and the `combiner` service on Debian / Raspberry Pi OS.

## Prerequisites

- Raspberry Pi with GbE (Pi 4/5 recommended; Pi 3 OK for early lab)
- Switch trunk and DHCP already set per **[`docs/setup.md`](../../docs/setup.md)**
- **Local console** (serial/HDMI) recommended — install disables NetworkManager/dhcpcd
- Edited `/etc/combiner/site.yaml` (start from `config/site.example.yaml`)

### Combiner DHCP (off by default)

Production Control DHCP stays on the core switch. Set `mgmt_dhcp.enabled: true` only when an optional **Mgmt** VLAN exists and you want the combiner to serve it.

```yaml
mgmt_dhcp:
  enabled: false
```

### Untagged Mgmt (native VLAN / flat lab LAN)

Lab boards on an access/native port (e.g. `virgil01` on `192.168.1.x`) use [`config/site.lab-flat.example.yaml`](../../config/site.lab-flat.example.yaml). That Mgmt face is a **Pi uplink**, not the client network. Production clients live on Control.

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
sudo cp config/site.example.yaml /etc/combiner/site.yaml   # or site.lab-flat.example.yaml
# edit /etc/combiner/site.yaml
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

`install.sh` uses the prebuilt `bin/` binaries and does not require Go.

**From a git checkout** (dev / cross-compile):

```bash
sudo mkdir -p /etc/combiner
sudo cp config/site.example.yaml /etc/combiner/site.yaml   # or site.lab-flat.example.yaml
sudo cp -r config/allowlists /etc/combiner/
# edit /etc/combiner/site.yaml
# ensure bin/combiner and bin/combiner-status exist (release package, make build-pi, or go on PATH)
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

Without `--i-have-console`, the script refuses to run over SSH (it would lock you out).

### What the installer does (order matters)

1. Leaves **IP forwarding off**
2. Generates nftables and runs **`nft -c -f`** (abort if invalid)
3. Loads a bootstrap **forward drop** ruleset, then the real ruleset
4. Flushes conntrack
5. Configures VLANs / dnsmasq / combiner
6. **Waits for every interface named in `site.yaml` to exist** — aborts with diagnostics if not
7. **Enables IP forwarding last**
8. Confirms `combiner` is still active a few seconds after start

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

```bash
sudo ./deploy/pi/generate-nftables.sh /etc/combiner/site.yaml /tmp/nft.conf
sudo nft -c -f /tmp/nft.conf
sudo cp /tmp/nft.conf /etc/nftables.conf
sudo nft -f /etc/nftables.conf
sudo conntrack -F || true
```
