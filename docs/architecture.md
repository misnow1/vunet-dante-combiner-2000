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

### What SNAT cannot carry

SNAT works for a protocol whose devices only ever **answer** the controller. It cannot carry one whose devices must **reach** the controller, or that checks for an on-subnet peer.

**Dante Controller is the second kind.** Discovery and some control survive SNAT, but the **metering tab and device configuration require L2 adjacency** and do not. Measured on a live rig: unicast to Dante devices on `udp/8700` is delivered and every flow stays `[UNREPLIED]`, with NAT applied correctly. This is an Audinate constraint, not a combiner defect — no ruleset change fixes it.

**Martin VuNET is the first kind**, and is the mirror image: discovery is multicast query/response on `239.254.10.2`, and control is client-initiated long-lived TCP to amp port `63489`, with no amp-originated traffic at all. It NATs cleanly.

So the two protocols want opposite sides of the combiner, and which one gets to be native is a deployment choice — see below. Details and captures: [`protocols.md`](protocols.md).

### Two profiles

`client_vlan` (default `control`) selects which side the control clients sit on. It moves the reflector's client side, the unicast forward direction, and the SNAT target.

| | `client_vlan: control` (default) | `client_vlan: dante` |
| --- | --- | --- |
| Clients live on | Control, with the amps | Dante Primary, with the Dante devices |
| Native (full function) | VuNET, mixer control, MixPad | **Dante Controller, Shure WWB, Lake** |
| Reflected + SNATed | Dante, Shure, Lake | **Martin VuNET** |
| Dante metering / device config | **Not available** | Works |
| Example | [`site.example.yaml`](../config/site.example.yaml) | [`site.dante-client.example.yaml`](../config/site.dante-client.example.yaml) |

Pick `dante` when the operator needs full Dante Controller. Keep the default when clients are **tablets on Wi-Fi**: that profile puts the WAP on Dante Primary, exposing PTP and any multicast audio to a medium that carries multicast at low basic rates — the same *class* of problem the design avoids for the amp stack.

What `client_vlan` deliberately does **not** move is the PTP/AES67/ATP deny direction. Those stay anchored to Control because they exist to keep the amp stack quiet. Letting them follow the client would drop PTP toward Dante and break the clock of the network the combiner exists to carry.

Install is fail-closed: nftables is validated and a drop-forward policy is loaded before IP forwarding is enabled.

```text
client_vlan: control (default)        client_vlan: dante
Control VLAN (PCs, amps, mixer)       Dante VLAN (PCs, Lake, Dante, Shure)
        |                                     |
        |  SNAT unicast +                     |  SNAT unicast +
        |  allowlisted mcast reflect          |  allowlisted mcast reflect
        v                                     v
Dante VLAN (Lake, Dante, Shure)       Control VLAN (amps, mixer control)
```

In both directions the PTP/media denies point at **Control** — they do not flip.

Physical ports and the WAP are in [`setup.md`](setup.md). SoundGrid stays on its own switch.

## Isolation

Roles below are **client** and **peer**, which follow `client_vlan`; under the default profile the client is Control and the peer is Dante.

| Path | Policy |
| --- | --- |
| Client → peer unicast | Allow + SNAT to the combiner's peer-side IP |
| Peer → client | Established/related only |
| Client ↔ peer multicast | Reflector allowlist only |
| PTP / ATP (UDP 4321) / AES67 → Control | Deny |
| SoundGrid | Not a combiner interface |

Reflected under the **default** profile (light vs PTP): mDNS, Dante `224.0.0.230`–`233`, Shure `239.255.254.253:8427`, plus Lake groups after capture. Under `client_vlan: dante` the single reflected group is VuNET `239.254.10.2:6002,54077` — Dante, Shure and Lake are all native to the client there and must not be listed.

The meeting point for the reflected side's apps is **software on the client**, not an L2 bridge. Do not use a switch SVI as a shortcut Control→Dante ([`setup.md`](setup.md) DHCP). Break-glass: [`break-glass.md`](break-glass.md).

Multiple Control clients are fine at the network layer. VuNET and Lake still allow only one “brain” app instance — that is an application limit.

## Data plane

| Layer | Role |
| --- | --- |
| `nftables` + `ip_forward` | Unicast SNAT, isolation, counters |
| Userspace reflector | Allowlisted multicast client↔peer |
| Core DHCP | Control clients; combiner does not DHCP Control or Dante |

## Non-goals

- Combiner as Wi-Fi AP
- DHCP on Control or Dante
- Broadcast reflection
- Bridging media/PTP, or attaching SoundGrid
- ESP32 / MCU platforms
