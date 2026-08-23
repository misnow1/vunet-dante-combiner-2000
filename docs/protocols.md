# Protocol research

Cited notes on how each control/audio protocol behaves on the wire, and what that means for the combiner. Operational allow/deny tables live in [`traffic-matrix.md`](traffic-matrix.md). Cabling and install: [`setup.md`](setup.md). This file is the *why*.

Laptops and iPads sit on **Martin Control** with VuNET, Yamaha mixer-control, and Allen & Heath MixPad. The combiner only stitches **Dante-side** discovery/control (Dante Controller, Lake, Shure WWB) onto that VLAN. **Waves SoundGrid** is a third island and is never trunked.

| Protocol | Combiner role |
| --- | --- |
| Dante discovery/control | Reflect allowlisted multicast Control↔Dante; SNAT unicast to Dante |
| Shure WWB (on Dante VLAN) | Same path; extra discovery group `239.255.254.253:8427` |
| Lake Controller | Dante VLAN; reflect after capture |
| VuNET / Martin | Native L2 on Control — **not reflected** (reflected only under `client_vlan: dante`) |
| Yamaha mixer control | Native L2 on Control — **not reflected** |
| Allen & Heath MixPad | Native L2 on Control — **not reflected** (UDP broadcast) |
| Dante PTP / ATP / AES67 | Hard deny toward Control (and optional Mgmt) |
| Waves SoundGrid / SoE | Never attach |

---

## Dante (Audinate)

**Sources:** Audinate network-admin documentation (control groups `224.0.0.230`–`233`, mDNS, PTP, ATP/AES67); seeded in [`config/allowlists/dante.yaml`](../config/allowlists/dante.yaml).

| Kind | Address / ports | Combiner |
| --- | --- | --- |
| Discovery | `224.0.0.251` UDP 5353 (mDNS / DNS-SD) | Reflect |
| Control / monitoring | `224.0.0.230`–`233` UDP 8700–8708 | Reflect |
| Unicast control | UDP 4440, 4444, 4455, 8800 | SNAT (nftables unicast) |
| Metering | UDP 8751 | SNAT; confirm in the field |
| Logging (optional) | `239.254.1.1`, `239.254.44.44` UDP 9998 | Reflect (allowlist); not media |
| Clock | `224.0.1.129`–`132` UDP 319/320 (PTP) | **Never** reflect or forward toward Control |
| Media | `239.255.0.0/16` **UDP 4321** (ATP); `239.69.0.0/16` UDP 5004 (AES67) | **Never** (ATP is port-matched so Shure `239.255.254.253:8427` can be reflected) |
| PTP logging | `239.254.3.3` UDP 9998 | Deny prefix |

Dante Controller learns device IPv4 from **payload**, not from the reflected packet’s outer source. The reflector rewrites outer src to the combiner address on the egress VLAN; that is OK for Dante.

`224.0.0.0/24` is link-local multicast. Many switches flood it even without IGMP. Volume is small compared with PTP and multicast audio.

---

## Shure Wireless Workbench

WWB rides the **Dante VLAN** in this design (same switch ports as Dante devices, by site constraint).

**Sources:**

- [WWB6 ports and protocol information](https://service.shure.com/articles/en_US/Knowledge/wwb6-ports-and-protocol-information)
- [Enterprise network troubleshooting checklist](https://service.shure.com/articles/en_US/Knowledge/enterprise-network-troubleshooting-checklist)
- [Shure Device IP Ports and Protocols (PDF)](https://content-files.shure.com/FileRepository/common-ip-ports-v2.pdf)

| Kind | Address / ports | Combiner |
| --- | --- | --- |
| Shure Discovery (multicast SLP) | `239.255.254.253` UDP **8427** | Reflect — [`config/allowlists/shure.yaml`](../config/allowlists/shure.yaml) |
| Bonjour / mDNS | `224.0.0.251` UDP 5353 | Already in Dante allowlist |
| Device control (SDT) | UDP **5568** unicast | SNAT |
| SNET (some families) | UDP **2200–2201** unicast | SNAT |
| SSDP | `239.255.255.250` UDP 1900 | **Not seeded** unless a capture shows WWB needs it |
| Firmware | TCP 21, 64000, etc. | Out of MVP; unicast SNAT would pass it |
| Dante clock/media from Shure cards | PTP / ATP / AES67 | Same hard denies as Dante |

Shure’s own guidance: devices expect same-VLAN multicast; Discovery should register via IGMP; mDNS and Dante control (`224.0.0.230`–`233`) often flood the subnet.

---

## VuNET / Martin (Linea Research amps)

Amp **control** must stay off Dante **PTP and media** (Martin best practice: PTP can overwhelm the amp stack). Light discovery multicast from Dante Controller / WWB on Control is accepted in this topology.

Under the **default** (control-client) profile, VuNET discovery groups are **not** a combiner allowlist. Clients share the Control broadcast domain with the amps, so VuNET works as in break-glass (native L2). Unicast is on-subnet; no SNAT. Adding a Control-VLAN group there would hairpin Control onto itself, and config rejects it.

Under [`config/site.dante-client.example.yaml`](../config/site.dante-client.example.yaml) (`client_vlan: dante`) the roles invert: clients sit on Dante for full Dante Controller function, and VuNET becomes the reflected protocol. [`config/allowlists/vunet.yaml`](../config/allowlists/vunet.yaml) is loaded **only** in that profile.

### Measured behaviour (12 WPC amps, 2026-08-23)

| Element | Value |
| --- | --- |
| Discovery group | `239.254.10.2`, UDP **6002** and **54077**, TTL **1** |
| Amp announce | 91-byte, source port == destination port |
| VuNET query | 23-byte, ephemeral source port |
| Control session | **TCP 63489**, client-initiated, long-lived, no teardown |

Discovery is query/response over the group, so the allowlist uses `direction: both`. TTL 1 is not an obstacle — the reflector re-originates rather than forwarding.

Control is **client-initiated TCP only**; amps never originate traffic toward the controller, which is why SNAT carries it cleanly (unlike Dante, which needs L2 adjacency for metering and device config).

VuNET also broadcasts a Lantronix probe (`00 00 00 F6` to `255.255.255.255:30718`) about every 3.4 s. **No amp ever answers it** — it probes for unrelated Lantronix hardware. It is not discovery and needs no broadcast relay.

A capture on Control is still useful to *document* VuNET groups for operators. See [`capture-playbook.md`](capture-playbook.md).

---

## Lake Controller

Lake control frames use **static IPv4 on the Dante subnet** in this design.

Discovery is still **capture-required** ([`config/allowlists/lake.yaml`](../config/allowlists/lake.yaml)). May be multicast, broadcast, and/or mDNS. Until capture:

- mDNS reflection Control↔Dante may help if Lake uses DNS-SD
- Unicast after discovery uses the Dante SNAT path

Do not reflect all broadcast.

---

## Yamaha mixer control (CL / QL / TF / similar)

**Sources:** [CL/QL Series System Design Guide](https://download.yamaha.com/files/tcm:39-1251310); CL/QL Editor supplementary manuals (NETWORK: FOR MIXER CONTROL vs FOR DEVICE CONTROL); Yamaha QLab remote-control notes (use the **LAN / NETWORK** port, not Dante).

Yamaha consoles expose **separate** networks:

| Port | Role |
| --- | --- |
| Dante | Audio, PTP, HA remote for R-series (Dante-side) |
| NETWORK / FOR MIXER CONTROL | CL/QL Editor, StageMix, MonitorMix |
| Device control (where present) | Other remote HA / Nuendo Live |

StageMix/Editor expect the iPad or PC **on the mixer-control subnet**. Yamaha does not publish a small multicast allowlist comparable to Dante. StageMix documentation typically has you attach the WAP to the mixer LAN and often type the console IP. RCP (Remote Control Protocol) itself is **TCP 49280** (unicast, IP already known).

**Combiner:** native L2 on Control. Do not seed Yamaha discovery groups from folklore. Dante-side HA remote (R Remote) stays on Dante and may benefit from existing Dante reflection.

---

## Allen & Heath (dLive / Avantis / AHM)

**Source:** [Allen & Heath for IT managers](https://support.allen-heath.com/hc/en-gb/articles/37287399691409-Allen-Heath-for-IT-managers).

Device discovery: each unit (and IP controller) **broadcasts** a UDP “find” about once per second (payload is name/type/software; on the order of 80–160 bytes). This is **not** IGMP multicast. If broadcast is blocked across a router, MixPad can connect by typing an IP.

After discovery, AH-Net uses TCP **51321** (rendezvous) plus UDP from **51324** upward per client. MIDI-over-TCP control uses TCP **51325** (and TLS variants 51327+ on dLive).

**Combiner:** native L2 on Control. The reflector is multicast-only; nftables drops forwarded `255.255.255.255`. Do **not** build a broadcast reflector.

---

## Waves SoundGrid and DiGiCo

**Sources:**

- [SoundGrid System Design Guidelines](https://www.waves.com/support/soundgrid-system-design-guidelines)
- [Network switches and cables for SoundGrid](https://www.waves.com/support/network-switches-cables-for-soundgrid-systems)
- [DiGiCo + Waves SoundGrid](https://digico.biz/interfaces/waves-soundgrid/)
- Quantum 7 MultiRack setup notes (Waves I/O, servers, and console “blue switch” on one qualified SG switch)

SoundGrid is a **private Gigabit L2 audio network**. Waves: do not mix other protocols on the same switch; use VLANs if the chassis is shared. Clock is proprietary **SoE (Sync-over-Ethernet)** on the **same links as audio**, not IEEE PTP `224.0.1.129`. Waves does not publish a small SoE group/port list suitable for nftables deny prefixes.

On DiGiCo + Waves, the console **control** path (MultiRack host, Waves I/O, SoundGrid servers) lives on that SoundGrid switch. SuperRack/DiGiCo ProLink can add more multicast (MLD snooping on that fabric). Still SoundGrid-only.

**Combiner:** never trunk, SNAT, or reflect SoundGrid. Isolation is **by not connecting**, not by guessing SoE addresses.

Putting SoundGrid onto Control so one SSID reaches both DiGiCo and VuNET would dump SoE and audio onto Martin amps and Wi-Fi — the same *class* of failure as bridging Dante PTP onto Control.

| DiGiCo layout | Where control clients live |
| --- | --- |
| Without SoundGrid (plain console control LAN) | Control VLAN, like Yamaha |
| With SoundGrid | SoundGrid island (or a dual-homed PC). Not the VuNET SSID |

---

## What the reflector will not do

- Copy UDP broadcast (A&H find, some Lake/VuNET patterns)
- Invent Yamaha or VuNET multicast groups
- Reflect SSDP or Shure firmware ports unless a capture demands it
- Weaken PTP / ATP / AES67 denies toward Control
- Touch SoundGrid / SoE / ProLink
