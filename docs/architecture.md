# Architecture

Why the combiner exists and what it is allowed to do. **Cabling, addresses, DHCP, and Pi install:** [`setup.md`](setup.md). Hop-by-hop packets: [`packet-flow.md`](packet-flow.md).

## Problem

Martin Audio best practice keeps amp **control** off Dante **PTP and multicast audio** — that traffic can overwhelm the amp control stack. The same FOH/monitor computer also runs VuNET, Yamaha or A&H mixer apps, Dante Controller, Lake, and often Shure WWB. Mixer-control and MixPad want **same-subnet** (including UDP broadcast). Dante/WWB/Lake live on the Dante VLAN. Dual-NIC or client VLAN tagging is what we are trying not to require.

## Solution

Clients and the WAP live on **Martin Control** with the amps. The combiner is a Linux box with an IP on Control and on Dante. It:

- **SNATs** client unicast toward Dante so Lake/Dante/Shure see the combiner’s Dante address (those devices often have no useful default route)
- **Reflects** allowlisted multicast discovery/control Control↔Dante (Dante, Shure, Lake after capture). Kernel `forward` never passes multicast
- **Drops** PTP and multicast media toward Control
- **Never** attaches **Waves SoundGrid** (SoE clock + audio — same *class* of problem as Dante PTP)

VuNET, StageMix/Editor, and MixPad stay **on-link** on Control (no SNAT, no reflector). A&H discovery is UDP broadcast; the combiner does not copy broadcast.

Install is fail-closed: nftables is validated and a drop-forward policy is loaded before IP forwarding is enabled.

```text
Control VLAN (PCs, amps, mixer-control)
        |  SNAT unicast + allowlisted mcast reflect
        v
Dante VLAN (Lake, Dante, Shure)
```

Physical ports and the WAP are in [`setup.md`](setup.md). SoundGrid stays on its own switch.

## Isolation

| Path | Policy |
| --- | --- |
| Control → Dante unicast | Allow + SNAT to combiner Dante IP |
| Dante → Control | Established/related only |
| Control ↔ Dante multicast | Reflector allowlist only |
| PTP / ATP (UDP 4321) / AES67 → Control | Deny |
| SoundGrid | Not a combiner interface |

Reflected onto Control (light vs PTP): mDNS, Dante `224.0.0.230`–`233`, Shure `239.255.254.253:8427`, plus Lake groups after capture.

The meeting point for Dante-side apps is **software on the Control client**, not an L2 bridge. Do not use a switch SVI as a shortcut Control→Dante ([`setup.md`](setup.md) DHCP). Break-glass: [`break-glass.md`](break-glass.md).

Multiple Control clients are fine at the network layer. VuNET and Lake still allow only one “brain” app instance — that is an application limit.

## Data plane

| Layer | Role |
| --- | --- |
| `nftables` + `ip_forward` | Unicast SNAT, isolation, counters |
| Userspace reflector | Allowlisted multicast Control↔Dante |
| Core DHCP | Control clients; combiner does not DHCP Control or Dante |

## Non-goals

- Combiner as Wi-Fi AP
- DHCP on Control or Dante
- Broadcast reflection
- Bridging media/PTP, or attaching SoundGrid
- ESP32 / MCU platforms
