# Traffic matrix

Default posture: **deny**, then allow only what the combiner must stitch. Clients live on **Martin Control**; VuNET / Yamaha mixer-control / A&H MixPad are native L2 there. The reflector copies **Dante + Shure** (and Lake after capture) onto Control. Install: [`setup.md`](setup.md). Protocol citations: [`protocols.md`](protocols.md).

Fill Lake (and optional VuNET documentation) from on-site captures ([`capture-playbook.md`](capture-playbook.md)).

## VLAN pair policy

| Src → Dst | Unicast | Multicast |
| --- | --- | --- |
| Control → Dante | Allow + SNAT to combiner Dante IP | **Never kernel-forward** — userspace reflector, allowlisted groups only |
| Dante → Control | Allow established/related (conntrack) | Reflect allowlisted groups only |
| Optional Mgmt → Control | Allow + SNAT to combiner Control IP (lab/status) | **Drop** in `forward` — reflector does not join Mgmt |
| Optional Mgmt → Dante | Allow + SNAT to combiner Dante IP (lab) | **Drop** in `forward` |
| Any → Control (or Mgmt) carrying PTP or media mcast | **Drop** | **Drop** |
| Any forwarded multicast (`224.0.0.0/4`) or limited broadcast | **Drop** in `forward` | Reflector is the sole cross-VLAN multicast path |
| Combiner ↔ SoundGrid | **Never attached** | **Never** |

New Control→Dante sessions are client-initiated (Dante Controller, WWB, Lake). Amps without a default route should not originate Dante unicast.

## Hard deny (never forward or reflect toward Control / Mgmt)

| Group / range | Ports | Purpose |
| --- | --- | --- |
| `224.0.1.129`–`224.0.1.132` | UDP 319, 320 | PTP (Dante clock) — encoded as `224.0.1.128/30` + `224.0.1.132/32` |
| `239.255.0.0/16` | UDP **4321** only | Dante ATP multicast audio — **not** a blanket /16 drop (Shure Discovery is `239.255.254.253:8427`) |
| `239.69.0.0/16` | UDP 5004 | AES67 multicast audio |
| `239.254.3.3` | UDP 9998 | PTP logging (if enabled) |

These denials protect Martin amp NICs and Control-SSID Wi-Fi. Discovery/control groups below **are** reflected onto Control by design (light compared with PTP).

**SoundGrid / SoE** is not a deny prefix: the combiner must not trunk that VLAN. See [`protocols.md`](protocols.md) § Waves SoundGrid.

## Dante — allow (seeded from Audinate docs)

See also [`config/allowlists/dante.yaml`](../config/allowlists/dante.yaml).

| Address | Ports | Type | Purpose |
| --- | --- | --- | --- |
| `224.0.0.251` | UDP 5353 | Multicast | mDNS / DNS-SD discovery |
| `224.0.0.230`–`224.0.0.233` | UDP 8700–8708 | Multicast | Dante control & monitoring |
| (unicast any) | UDP 4440, 4444, 4455 | Unicast | Audio control |
| (unicast any) | UDP 8751 | Unicast | Dante Controller metering |
| (unicast any) | UDP 8800 | Unicast | Control & monitoring |

Unicast rows are SNAT/forwarding, not the multicast reflector. Metering (8751) is allowed so a clean path to “full glass” exists; confirm in the field.

**Do not reflect** ATP/AES67 media groups or PTP.

## Shure Wireless Workbench — allow (Dante VLAN)

WWB rides Dante with the wireless gear. See [`config/allowlists/shure.yaml`](../config/allowlists/shure.yaml) and [`protocols.md`](protocols.md).

| Address | Ports | Type | Purpose |
| --- | --- | --- | --- |
| `239.255.254.253` | UDP 8427 | Multicast | Shure Discovery / multicast SLP |
| `224.0.0.251` | UDP 5353 | Multicast | mDNS (shared with Dante allowlist) |
| (unicast any) | UDP 5568 | Unicast | SDT device control |
| (unicast any) | UDP 2200–2201 | Unicast | SNET (some families) |

Do **not** seed SSDP (`239.255.255.250:1900`) unless a capture shows WWB needs it. Shure Dante cards still originate PTP/media — those stay on the deny floor.

## VuNET / Martin — native Control (not reflected)

Clients share the Control VLAN with the amps. No VuNET allowlist on the reflector.

| Address | Ports | Type | Purpose | Status |
| --- | --- | --- | --- | --- |
| (on-link) | vendor | Unicast / whatever VuNET uses | Amp control | Native L2 |
| _optional capture_ | _TBD_ | Multicast / broadcast | Documentation only | [`capture-playbook.md`](capture-playbook.md) |

## Lake Controller — allow (capture required)

Lake control rides the **Dante** VLAN (static IPv4 on the Dante subnet).

| Address | Ports | Type | Purpose | Status |
| --- | --- | --- | --- | --- |
| _TBD_ (often broadcast and/or mDNS-related) | _TBD_ | Multicast / broadcast | Device discovery | Stub — capture on Dante VLAN with Lake Controller open |
| (unicast any) | _TBD_ | Unicast | Device control | SNAT path |

Placeholder: [`config/allowlists/lake.yaml`](../config/allowlists/lake.yaml).

Until Lake groups are known, mDNS (`224.0.0.251`) reflection on Control↔Dante may help if Lake uses DNS-SD; verify with capture. Do not reflect all broadcast.

## Console control on Control (not reflected)

| Vendor | Discovery | Unicast (typical) | Combiner |
| --- | --- | --- | --- |
| Yamaha mixer-control (StageMix / Editor) | Same subnet as NETWORK / FOR MIXER CONTROL; not a published mcast allowlist | TCP 49280 (RCP) | Native L2 |
| Allen & Heath MixPad | UDP **broadcast** “find” ~1 Hz | AH-Net TCP 51321 + UDP from 51324; MIDI TCP 51325 | Native L2; broadcast cannot be reflected |
| DiGiCo **without** SoundGrid | Console control LAN | Vendor | Native L2 on Control |
| DiGiCo **with** SoundGrid | Control NIC is on the SoundGrid fabric (SoE + audio) | MultiRack on SG | **Out of scope** — do not merge onto Control |

## ICMP / ARP

- ARP stays local to each VLAN.
- ICMP echo Control↔Dante: allow (debug).
- ICMP echo optional Mgmt↔Control / Mgmt↔Dante: allow.
- Limited broadcast (`255.255.255.255`) never kernel-forwards.
