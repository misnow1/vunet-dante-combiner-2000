# Architecture

## Problem

Martin Audio best practice keeps amp **control** off the **Dante** network because Dante PTP (and related multicast) can overwhelm the amp control stack and cause reboots. That forces a control computer to sit in both VLANs — two NICs, or VLAN tagging Windows Home lacks — and blocks a simple wireless-only workflow.

Martin amps (Linea Research) do not expose a usable default route under static VuNET addressing. Dante device gateway fields may be empty or wrong. Discovery for VuNET, Dante Controller, and Lake Controller is multicast-based.

## Solution

A small Linux **combiner** on an 802.1Q trunk:

1. Creates a third **Mgmt** VLAN for control clients (laptop/tablet via a separate WAP or wired port).
2. Holds an IP on Mgmt, Control, and Dante.
3. Forwards **only** Mgmt↔Control and Mgmt↔Dante.
4. **SNATs** egress toward Control/Dante so devices see an on-subnet source.
5. **Reflects** allowlisted multicast discovery/control onto Mgmt (and client multicasts back). Kernel `forward` **never** passes multicast — the reflector is the only cross-VLAN multicast path.
6. **Drops** PTP and multicast media toward Mgmt and Control from **any** source; never forwards Control↔Dante.
7. Install is **fail-closed**: validate nftables (`nft -c`) and load drop-forward rules before enabling IP forwarding.

Clients use the combiner as their default gateway (DHCP on Mgmt). Apps behave as if they had a foot in each production VLAN.

```text
  [Tablet/Laptop] --Wi-Fi--> [WAP] --untagged Mgmt--> [Switch]
                                                         |
                                              trunk (Mgmt+Control+Dante)
                                                         |
                                                   [Combiner Pi]
                                                         |
                    +--------------------+---------------+
                    |                    |
             Martin Control VLAN    Dante VLAN
                    |                    |
              Martin amps         Lake / Dante gear
```

## Why SNAT (“same-subnet lie”)

Amps often cannot reply off-subnet. Rather than teaching every device a gateway, the combiner:

- Owns a static address on Control and on Dante
- Masquerades client traffic so the **source IP** is the combiner’s address on that VLAN
- Conntrack reverses the translation on replies back to Mgmt clients

Multiple Mgmt clients are fine at the network layer. VuNET and Lake Controller still allow only one “brain” application instance — that is an app constraint, not a combiner feature.

## Hard isolation

| Path | Policy |
| --- | --- |
| Mgmt ↔ Control | Allow **unicast** (SNAT); multicast via reflector allowlist only |
| Mgmt ↔ Dante | Allow **unicast** (SNAT); multicast via reflector allowlist only |
| Control ↔ Dante | **Deny** |
| Any forwarded multicast | **Deny** in nftables `forward` |
| PTP / media → Mgmt or Control | **Deny** from any source (counters expected non-zero when Dante is busy) |

The combiner must not become a sneaky bridge between Control and Dante. The meeting point is the **client software on Mgmt**, not L2/L3 between those VLANs.

## Data plane split

| Layer | Responsibility |
| --- | --- |
| Kernel (`nftables`, VLAN ifaces, ip_forward) | Unicast SNAT, isolation, hard drops, counters |
| Userspace reflector | Join/reflect allowlisted multicast groups; inventory discovered peers |
| `dnsmasq` | DHCP **only** on Mgmt |

## Intentional routing authority

Switch SVIs may exist for convenience. Do **not** use them as the control path for these apps. Mgmt clients must use the combiner as gateway. If the combiner fails, use break-glass direct attachment (see [`break-glass.md`](break-glass.md)).

## MVP vs follow-on

| Capability | MVP | Follow-on |
| --- | --- | --- |
| VuNET / Dante / Lake discovery + control unicast | Yes | — |
| Dante Clock Status / sync health | Yes | — |
| Dante live metering | Path cleared in nftables | Confirm end-to-end |
| Status page + discovered hosts | Yes | — |
| PoE / Sipeed GbE appliance | Docs only | Hardware eval |

## Non-goals

- ESP32 / MCU platforms
- Combiner as Wi-Fi AP (use a separate WAP on Mgmt)
- DHCP on Control or Dante
- Relying on switch L3 or device default routes
- Bridging Control↔Dante for convenience
