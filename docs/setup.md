# Set up a combiner

This is the document to follow if you are putting a combiner on a show network. It covers addresses, switch ports, DHCP, installing on a Raspberry Pi, and a first check that it is alive.

How the box forwards packets, nftables, and the multicast reflector: [`architecture.md`](architecture.md) and [`packet-flow.md`](packet-flow.md). If the combiner dies later: [`break-glass.md`](break-glass.md).

## What you are building

Laptops and iPads sit on **Dante Primary**, with the Lake, Dante and Shure gear. That is what makes Dante Controller fully functional — its metering tab and device config need L2 adjacency, which no amount of forwarding can supply. Shure WWB and Lake Controller are native there too.

The combiner sits on a trunk to Dante **and** Martin Control, and carries **VuNET** the other way, so those same clients can reach the Martin amps **without** putting Dante PTP or multicast audio onto Control.

This is the `client_vlan: dante` profile, which is what both shipped configs use. The alternative — clients on Control, Dante reflected to them, no Dante metering — is described in [`architecture.md`](architecture.md#two-profiles) and ships no example.

The combiner does **not** run DHCP on Control or Dante. You configure that on the core.

**Do not** connect Waves SoundGrid (DiGiCo + Waves) to this trunk or to the Control SSID.

## 1. Addresses

Pick the profile that matches your combiner port, copy it to `/etc/combiner/site.yaml`, and set real VLAN IDs:

| Combiner port | Profile |
| --- | --- |
| **Audio trunk** — PVID/untagged Dante carrying clients, tagged Control carrying amps | [`config/site.example.yaml`](../config/site.example.yaml) **(production)** |
| Fully tagged — no untagged VLAN on the port | `site.example.yaml`, with the one-line change its `dante:` block documents |
| Flat lab LAN — untagged Mgmt uplink, Control/Dante tagged | [`config/site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml) |

Example prefixes (`/21` on the audio nets):

| Role | Example VLAN ID | Example prefix | Combiner IP | Interface (shipped profile) | Who else |
| --- | --- | --- | --- | --- | --- |
| Martin Control | `200` | `10.200.0.0/21` | **`10.200.0.1`** | `eth0.200` (tagged) | Martin amps, mixer-control NICs |
| Dante Primary | `201` | `10.201.0.0/21` | **`10.201.0.1`** | `eth0` (untagged / PVID) | **PCs and iPads**, Lake, Dante devices, Shure receivers |

Exactly **one** VLAN may be untagged — a port has one PVID. In the shipped profile that is Dante, so its address lands directly on `eth0` and no `eth0.201` is created. Set it with `untagged: true`; keep `id: 201` anyway, since it documents the PVID and is still checked for uniqueness. Other tagged VLANs the port happens to carry are ignored — no interface is created for them, so the kernel drops those frames.

If a switch SVI already owns `.1`, pick the next free addresses and use **those** combiner IPs everywhere below (DHCP option 3, reservations). Keep combiner addresses **out of DHCP pools**.

Lab-only extra: a Pi uplink on a flat LAN uses [`config/site.lab-flat.example.yaml`](../config/site.lab-flat.example.yaml) (optional untagged Mgmt). That is not the client network.

## 2. Switch ports

```text
 Control VLAN                                      Dante VLAN
 [iPad / PC]                                       [Lake / Dante / Shure]
      |                                                    |
    Wi-Fi                                           access, untagged Dante
      v                                                    v
   [WAP] ---- access, untagged Control --+                 |
                                         |                 |
 [amps / mixer-control] -- access -------+---- [Switch] ---+
                                         |        |
                                         |   audio trunk: untagged/PVID Dante
                                         |               + tagged Control
                                         |        |
                                         |   [Combiner]

 SoundGrid (if present): its own switch — not this trunk, not the Control SSID
```

| Port | Untagged / PVID | Tagged | Notes |
| --- | --- | --- | --- |
| **Combiner** | **Dante** (audio trunk, shipped profile) — or an unused dummy VLAN on a fully tagged port | Control (plus Dante if the port is fully tagged) | Gigabit. PoE optional. No SoundGrid. |
| **WAP** | Control | none | Same L2 as amps. Allow **broadcast** (MixPad). |
| Wired FOH laptop | Control | none | |
| Amp / mixer-control | Control | none | As today |
| Dante / Lake / Shure | Dante | none | No WAP on Dante |

Do not use one SSID for Control and Dante.

### Audio trunk ports

Sites that already run "audio trunk" ports — PVID 201 untagged with 200 (and others) tagged — need no switch change: drop the combiner on any such port and the shipped profile matches it. Tagging does not change the data plane. Every nftables rule and every reflector membership keys on the interface *name*, so untagged Dante on `eth0` behaves exactly like tagged Dante on `eth0.201`.

One caveat before you patch it in: **SSH and the status page are accepted on Control only** (`management_access`, below). On a real audio trunk that is fine, because Control is tagged and present. If the combiner lands on a plain PVID-201 **access** port with no tagged 200, `eth0.200` exists but carries nothing, and the box is reachable only by console — ICMP echo still answers on every interface, so a successful ping to the Dante address with no SSH is the signature of that mistake.

### Management access

| `management_access` | SSH + status page reachable from |
| --- | --- |
| omitted (default) | Control, plus Mgmt when one is configured |
| `[control, dante]` | Control and Dante |
| `[dante]` | Dante only |

An explicit list is authoritative, so naming `[control, dante]` on a box that also has Mgmt drops SSH via Mgmt. Widening to Dante exposes port 22 and the status page to the whole audio VLAN — worth it if you may land on an access port, otherwise leave it alone.

Until the combiner port is a real trunk, its Control interface (and Dante, on a fully tagged port) may show `no-carrier`. That is expected.

## 3. DHCP

Serve DHCP from the **core**. Do not enable combiner DHCP on Control or Dante (`mgmt_dhcp.enabled: false` in the production example).

The clients live on **Dante**, so that is the scope that needs a gateway.

### Dante (where the clients are)

| Option | Name | Value |
| --- | --- | --- |
| 3 | Router | **Combiner Dante IP** (example `10.201.0.1`) — **laptops and iPads** that run VuNET |
| 6 | DNS | **Omit** |
| 1 | Subnet mask | Dante prefix (example `255.255.248.0`) |
| — | Pool | Exclude combiner Dante IP (and any SVI) |

**Dante devices, Lake and Shure receivers** should not get that default gateway. They only talk on-link, and Dante gear is meant to have no gateway at all. Prefer static/no-GW, or a DHCP class that omits option 3. A shared pool with option 3 for everyone is the tradeoff: clients reach the amps; Dante devices that honor a gateway get a route they should not use.

**Do not** set option 3 to a switch SVI that routes into Control.

### Control (where the amps are)

| Option | Name | Value |
| --- | --- | --- |
| 3 | Router | **Absent** |
| 6 | DNS | Omit |
| — | Pool | Exclude combiner Control IP |

The amps need no gateway: the combiner SNATs VuNET traffic so they only ever see an on-subnet peer.

Empty/static gateway on Dante gear is normal. That is why the combiner SNATs.

## 4. Install software on the Pi

Prefer a **Pi 4/5** with Gigabit Ethernet. Pi 3 works for lab at 100 Mbps.

1. On the Pi, `uname -m`: `aarch64` → `linux-arm64` release; `armv7l` → `linux-arm`; `x86_64` → `linux-amd64`.
2. Download the matching tarball from [Releases](https://github.com/misnow1/vunet-dante-combiner-2000/releases) (no Go required).
3. Extract, copy the profile from step 1 (`config/site.example.yaml` for an audio trunk port) to `/etc/combiner/site.yaml`, edit VLAN IDs and combiner addresses to match steps 1–3.
4. Run the installer from a **serial/HDMI console** (it disables NetworkManager/dhcpcd). Over SSH you must pass `--i-have-console` and accept that you may lock yourself out.

```bash
# Example arm64 — replace VERSION (no leading v)
curl -fsSL -o combiner.tgz \
  https://github.com/misnow1/vunet-dante-combiner-2000/releases/download/vVERSION/vunet-dante-combiner-VERSION-linux-arm64.tar.gz
tar -xzf combiner.tgz
cd vunet-dante-combiner-VERSION-linux-arm64

sudo mkdir -p /etc/combiner
sudo cp config/site.example.yaml /etc/combiner/site.yaml   # or site.lab-flat
# edit /etc/combiner/site.yaml
sudo ./deploy/pi/install.sh /etc/combiner/site.yaml --i-have-console
```

The installer only reaches for `apt` when something is actually missing, so a Pi
that was prepared at the bench installs with no Internet. If packages *are*
missing and there is no mirror, it names them and stops without changing
anything — stage them with `--offline-debs DIR` ([`pi-prep.md`](pi-prep.md)).

Building binaries yourself, the `virgil` lab board, and Go: [`pi-prep.md`](pi-prep.md). Installer failure modes: [`../deploy/pi/README.md`](../deploy/pi/README.md).

Prefer not to run the installer by hand? [`sd-image.md`](sd-image.md) prepares a
card at the bench that provisions itself on first boot — validated before it is
written. That unit boots twice: the first boot provisions on DHCP and holds,
still reachable, and `combiner-go-live` applies the config and reboots into it.

## 5. Check that it works

On the combiner:

```bash
combiner -version                                # which build is this
combiner -check -config /etc/combiner/site.yaml   # preflight: config + interfaces
ip -br addr
sudo combiner-status
curl -s http://127.0.0.1:8080/api/status | head
```

`-check` is also the safe way to edit `site.yaml` later — it exits non-zero on a bad config, so `combiner -check -config /etc/combiner/site.yaml && sudo systemctl restart combiner` will not restart into a crash loop. Unknown or misspelled keys are hard errors: `untaged: true` fails instead of quietly configuring the wrong tagging.

From a client: ping the combiner Dante IP; open `http://<combiner-dante-ip>:8080/`. Dante Controller / WWB / Lake should see devices **without** the combiner — they are native on this VLAN. VuNET needs the combiner (discovery reflection + SNAT). If Dante Controller works but VuNET does not, option 3 and the Dante gateway are the first things to check.

In the shipped profile `ip -br addr` shows the Dante address on `eth0` and Control on `eth0.200` — there is no `eth0.201`, and that is correct.

On a live Dante network, PTP drop counters may be non-zero. That is healthy.

## Lab without a production trunk

Use `site.lab-flat.example.yaml`: untagged Mgmt on the Pi NIC for the home LAN, Control and Dante still tagged (down until the port is a trunk). Leave `mgmt_dhcp.enabled: false`. Reserve the Pi’s Mgmt address in the LAN router. `vlans.mgmt.gateway` / `dns` are for the **Pi’s** uplink only.

## After it is up

- Status page: `http://<combiner-control-ip>:8080/`
- Combiner down: [`break-glass.md`](break-glass.md)
- Confirm Dante/Shure groups or capture Lake: [`capture-playbook.md`](capture-playbook.md)

## How it works (optional)

| Document | Contents |
| --- | --- |
| [`architecture.md`](architecture.md) | Why SNAT, isolation rules, what is in/out of scope |
| [`packet-flow.md`](packet-flow.md) | Discovery and unicast hop-by-hop |
| [`traffic-matrix.md`](traffic-matrix.md) | Allow/deny groups and ports |
| [`protocols.md`](protocols.md) | Vendor protocol notes (Dante, Shure, Yamaha, A&H, SoundGrid) |
