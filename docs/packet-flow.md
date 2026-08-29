# Packet flow (production addressing)

How discovery and unicast control move through the combiner. Assumes a basic grasp of Linux forwarding, VLAN subinterfaces, and IPv4.

**Cabling, addresses, DHCP, install:** [`setup.md`](setup.md). Why: [`architecture.md`](architecture.md). Allow/deny: [`traffic-matrix.md`](traffic-matrix.md). Vendor notes: [`protocols.md`](protocols.md).

Walkthroughs below use the shipped profile, [`config/site.example.yaml`](../config/site.example.yaml) (`client_vlan: dante`, `/21` audio nets): Dante combiner `10.201.0.1`, Control combiner `10.200.0.1`, PC `10.201.0.10`, Lake LM44 `10.201.2.162`, Martin iK42 `10.200.1.35`. If your site uses other combiner IPs, substitute them.

**The client is on Dante.** That is what makes Dante Controller fully functional — metering and device config need L2 adjacency. The combiner exists to carry **Martin VuNET** the other way, to the amps on Control. If your site runs the opposite arrangement (`client_vlan: control`), every direction below reverses; see [`architecture.md`](architecture.md#two-profiles).

Interface names are written `eth0.C` / `eth0.D` because tagging is a per-site choice. On the shipped **audio trunk** profile Dante is the port's untagged/PVID VLAN, so `eth0.D` is really `eth0` and only Control gets a `.200` subinterface; on a fully tagged trunk they are `eth0.200` and `eth0.201`. Nothing in the packet path changes — every rule keys on interface *name*, not on whether the frame carried a tag.

```text
Dante Primary 10.201.0.0/21                  Martin Control 10.200.0.0/21
┌──────────────────────────────┐            ┌──────────────────────────────┐
│ PC 10.201.0.10               │            │ iK42 amp 10.200.1.35         │
│   VuNET + Dante Ctrl + WWB   │  L3 SNAT   │ Combiner eth0.C 10.200.0.1   │
│ Lake LM44 10.201.2.162       │◄──────────►│                              │
│ Combiner eth0.D 10.201.0.1   │            └──────────────────────────────┘
└──────────────────────────────┘
```

On the Pi: one physical NIC, Dante + Control VLAN faces, `ip_forward=1`, nftables table `inet combiner`, and the userspace **reflector** joined to allowlisted multicast groups on both faces.

**Critical split**

| Kind | Path | Transform |
| --- | --- | --- |
| Multicast discovery (VuNET) | **INPUT** on one VLAN → reflector → **OUTPUT** on the other | New UDP datagram; payload copied; headers rewritten |
| Unicast to Martin amps | Kernel **FORWARD** Dante→Control + **SNAT** | Conntrack; src becomes combiner Control IP |
| Unicast Dante Controller / WWB / Lake | Local to Dante (switch L2) | None |
| PTP / media → Control | **Dropped** | Protects Martin amp control stacks |

Kernel `forward` **never** passes `224.0.0.0/4` or `255.255.255.255`. If multicast shows up in `drop_forward_mcast`, that is working as designed.

---

## Part 1 — Device discovery (multicast)

VuNET is the concrete example, because it is the protocol this profile reflects and the one with measured behaviour ([`protocols.md`](protocols.md#measured-behaviour-12-wpc-amps-2026-08-23)): group `239.254.10.2`, UDP **6002** and **54077**, TTL **1**.

### 1a. Martin amps announce on Control

**Intent:** the PC on Dante must hear amp-side discovery.

```text
iK42 10.200.1.35
  │  UDP multicast
  │  src 10.200.1.35, src port == dst port, 91-byte
  │  dst 239.254.10.2:6002
  │  L2: mcast MAC, Control VLAN tag
  ▼
Switch (Control flood / IGMP)
  ▼
Combiner eth0.C   ← INPUT (not FORWARD)
  │  nftables: UDP to 224/4 allowed after PTP/media input denies
  │  Reflector socket joined 239.254.10.2 on eth0.C
  │
  │  Userspace reflector:
  │    - cm.Dst = 239.254.10.2, ingress = eth0.C
  │    - inventory: 10.200.1.35 on vlan=control
  │    - builds a NEW UDP datagram (does not "route" the old one)
  │    - egress iface = eth0.D (Dante)
  │    - TTL: 1 for this group (as measured); 255 for mDNS; else 32
  │    - src IP = 10.201.0.1 (combiner on Dante)
  │    - src port = reflector listen port
  │    - dst still 239.254.10.2:6002
  │    - payload UNCHANGED (records inside still say 10.200.1.35)
  ▼
Dante VLAN → PC 10.201.0.10
```

**Header mangling**

| Field | On Control (original) | On Dante (after reflect) |
| --- | --- | --- |
| VLAN | Control | Dante |
| Src MAC | amp / switch | Combiner Dante MAC |
| Src IP | `10.200.1.35` | **`10.201.0.1`** |
| Src UDP port | amp's (== dst port) | Reflector bound port |
| Dst IP:port | `239.254.10.2:6002` | same |
| TTL | 1 as sent | forced per-group policy |
| Payload | device identity | **byte-identical** |

VuNET learns the amp address from the **payload**, not from the outer source IP.

### 1b. PC multicast toward Control

```text
PC 10.201.0.10 → dst 239.254.10.2:6002 (23-byte query, ephemeral src port)
  ▼
Combiner eth0.D (INPUT) → reflector → eth0.C
  new packet: src 10.200.0.1, dst 239.254.10.2:6002, payload same
  ▼
iK42 / other Martin amps
```

### 1c. Dante Controller / WWB / Lake

No reflector. Discovery and unicast stay on Dante, where the client already is — that is the point of this profile. A&H MixPad **broadcast** find never crosses VLANs (nftables drops forwarded `255.255.255.255`).

### 1d. What never reaches Control

PTP (`224.0.1.129–132` UDP 319/320), Dante ATP / AES67 media ranges, etc. are dropped toward Control in nftables and refused by the reflector using `deny_multicast_prefixes` from `site.yaml`. Audio multicast stays on Dante. SoundGrid/SoE is not on this box.

These denies stay anchored to **Control** whichever way `client_vlan` points. They exist to keep the amp stack quiet, not to protect whichever side the client happens to be on.

---

## Part 2 — Unicast control (after discovery)

The PC's default gateway is **`10.201.0.1`**. Apps unicast to real device IPs. On-link Dante destinations (Lake, Dante gear, Shure receivers) never hit the combiner. Off-link Control destinations do.

### 2a. PC → Martin iK42

**A. PC sends (Dante)**

```text
src 10.201.0.10:54321
dst 10.200.1.35:63489     (measured VuNET control session, TCP)
L2 dest = MAC of 10.201.0.1 (gateway ARP)
```

**B. Combiner routing**

Dest is not local → **FORWARD** `eth0.D` → `eth0.C`. Unicast accepted after PTP / multicast-forward drops.

**C. SNAT (postrouting)**

```text
Before:  10.201.0.10:54321 → 10.200.1.35:63489
After:   10.200.0.1:54321  → 10.200.1.35:63489
```

**D. On Control** — the amp sees neighbor `10.200.0.1` and replies there. Conntrack un-SNATs back to `10.201.0.10`.

| Hop | Src | Dst |
| --- | --- | --- |
| Dante, PC → combiner | `10.201.0.10` | `10.200.1.35` |
| Control, combiner → amp | **`10.200.0.1`** (SNAT) | `10.200.1.35` |
| Control, amp → combiner | `10.200.1.35` | `10.200.0.1` |
| Dante, combiner → PC | `10.200.1.35` | `10.201.0.10` |

VuNET sessions are long-lived and never torn down explicitly, so these conntrack entries persist for the life of the show. That is why `combiner-apply` flushes conntrack when the ruleset changes — see [`deploy/pi/README.md`](../deploy/pi/README.md).

### 2b. PC → Lake LM44

Same subnet — switch L2 only. Combiner is not in the path.

```text
PC 10.201.0.10 → 10.201.2.162  (ARP for the Lake, not the combiner)
```

### 2c. Both apps at once

```text
                 Dante Ctrl / WWB / Lake ──L2──► Dante gear
PC 10.201.0.10 ─┤
                 VuNET ──SNAT──► 10.200.0.1 ──► Control VLAN (amps)
```

---

## Part 3 — Sequence (discovery then VuNET unicast)

```mermaid
sequenceDiagram
  participant Amp as IK42_10_200_1_35
  participant CC as Combiner_Control_10_200_0_1
  participant CD as Combiner_Dante_10_201_0_1
  participant PC as PC_10_201_0_10
  participant Lake as Lake_10_201_2_162

  Note over Amp,PC: Control-side discovery multicast reflected not forwarded
  Amp->>CC: mcast src=amp dst=239.254.10.2
  CC->>CD: userspace reflect src=10.201.0.1 payload unchanged
  CD->>PC: mcast on Dante

  Note over PC,Amp: Unicast VuNET control SNAT to Control
  PC->>CD: src=10.201.0.10 dst=10.200.1.35
  CD->>CC: route to Control iface
  CC->>Amp: SNAT src=10.200.0.1 dst=amp
  Amp->>CC: reply dst=10.200.0.1
  CC->>CD: unSNAT dst=10.201.0.10
  CD->>PC: reply

  Note over PC,Lake: Dante apps stay on Dante
  PC->>Lake: on-link unicast
  Lake->>PC: on-link reply
```

---

## Part 4 — Linux path cheat-sheet

| Traffic | Component | Transform |
| --- | --- | --- |
| Discovery mcast in | `input` on Dante or Control + reflector | `ReadFrom` |
| Discovery mcast out | Reflector `WriteTo` | New packet; src = combiner on egress; TTL policy; payload copy |
| Unicast PC → amp | `forward` + `postrouting` SNAT | Src → combiner Control IP |
| Unicast amp → PC | `forward` + conntrack un-SNAT | Dst restored to PC |
| Unicast PC → Lake / Dante gear | L2 on Dante | None |
| PTP/media → Control | Early `forward` drop | `drop_ptp` / `drop_deny_mcast` |
| Any mcast in `forward` | Drop | `drop_forward_mcast` |

**ARP is per-VLAN.** The PC ARPs for `10.201.0.1` only when the destination is off-subnet (Control). Lake and Dante gear ARP for the PC directly. Martin amps ARP for the combiner's Control address, never for `10.201.0.10`.

---

## Part 5 — Why SNAT is required on Control

Without SNAT, a forwarded packet would still carry `src=10.201.0.10` onto Control. Amps with empty or wrong gateways will not reply off-subnet. SNAT makes the amp's session look like:

> "I am talking to my neighbor `10.200.0.1`."

This is also why the amps need **no** DHCP option 3 ([`setup.md`](setup.md#control-where-the-amps-are)): they never have to route anywhere, because the only off-VLAN peer they ever see has already been rewritten to look local.
