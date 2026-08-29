# Traffic matrix

Default posture: **deny**, then allow only what the combiner must stitch.

In the shipped profile (`client_vlan: dante`) clients live on **Dante Primary**: Dante Controller, Shure WWB and Lake are native L2 there and need no allowlist at all. The reflector copies **Martin VuNET** the other way, onto Dante, so the same client can reach the amps. That is the *only* allowlist either shipped config loads.

The `client_vlan: control` alternative inverts this — clients on Control, with Dante/Shure/Lake reflected to them — and its allowlists are documented further down, marked as such. Which profile you are on decides which half of this page applies. Install: [`setup.md`](setup.md). Protocol citations: [`protocols.md`](protocols.md). Why: [`architecture.md`](architecture.md#two-profiles).

## VLAN pair policy

Rows read for the shipped `client_vlan: dante` profile; under `client_vlan: control` the client and peer roles swap.

| Src → Dst | Unicast | Multicast |
| --- | --- | --- |
| Dante → Control (**client → peer**) | Allow + SNAT to combiner Control IP | **Never kernel-forward** — userspace reflector, allowlisted groups only |
| Control → Dante (**peer → client**) | Allow established/related (conntrack) | Reflect allowlisted groups only |
| Optional Mgmt → Control | Allow + SNAT to combiner Control IP (lab/status) | **Drop** in `forward` — reflector does not join Mgmt |
| Optional Mgmt → Dante | Allow + SNAT to combiner Dante IP (lab) | **Drop** in `forward` |
| Any → Control (or Mgmt) carrying PTP or media mcast | **Drop** | **Drop** |
| Any forwarded multicast (`224.0.0.0/4`) or limited broadcast | **Drop** in `forward` | Reflector is the sole cross-VLAN multicast path |
| Combiner ↔ SoundGrid | **Never attached** | **Never** |

New client→peer sessions are client-initiated — VuNET, in the shipped profile. Martin amps have no default route and should never originate cross-VLAN unicast; SNAT is what makes that unnecessary, because every session they see already looks on-subnet.

## Hard deny (never forward or reflect toward Control / Mgmt)

| Group / range | Ports | Purpose |
| --- | --- | --- |
| `224.0.1.129`–`224.0.1.132` | UDP 319, 320 | PTP (Dante clock) — encoded as `224.0.1.128/30` + `224.0.1.132/32` |
| `239.255.0.0/16` | UDP **4321** only | Dante ATP multicast audio — **not** a blanket /16 drop (Shure Discovery is `239.255.254.253:8427`) |
| `239.69.0.0/16` | UDP 5004 | AES67 multicast audio |
| `239.254.3.3` | UDP 9998 | PTP logging (if enabled) |

These denials protect Martin amp NICs, and they stay anchored to **Control** whichever way `client_vlan` points — they exist to keep the amp stack quiet, not to protect whichever side the client is on. Discovery/control groups below **are** reflected by design (light compared with PTP).

**SoundGrid / SoE** is not a deny prefix: the combiner must not trunk that VLAN. See [`protocols.md`](protocols.md) § Waves SoundGrid.

## VuNET / Martin — allow (the shipped profile's only allowlist)

Under `client_vlan: dante` the amps are the reflected peer. Measured on a live rig (12 WPC amps, 2026-08-23) — see [`protocols.md`](protocols.md#measured-behaviour-12-wpc-amps-2026-08-23) and [`config/allowlists/vunet.yaml`](../config/allowlists/vunet.yaml).

| Address | Ports | Type | Purpose |
| --- | --- | --- | --- |
| `239.254.10.2` | UDP 6002, 54077 | Multicast | Amp discovery — TTL 1; amps announce 91-byte with src port == dst port, VuNET queries 23-byte from an ephemeral port |
| (unicast any) | TCP **63489** | Unicast | Amp control session — client-initiated, long-lived, no explicit teardown |

The allowlist entry is `vlan: control` because the field names the **peer** role, not the client's. Under `client_vlan: control` this group must **not** be allowlisted: clients would already share Control with the amps, so reflecting it would hairpin Control onto itself, and the config loader rejects it.

## Dante — allow (`client_vlan: control` only)

> Not loaded by either shipped profile. Under `client_vlan: dante` the client is already on Dante and needs none of this reflected; the loader rejects it.

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

## Shure Wireless Workbench — allow (`client_vlan: control` only)

> Not loaded by either shipped profile — WWB is native to the client's own VLAN there.

WWB rides Dante with the wireless gear. See [`config/allowlists/shure.yaml`](../config/allowlists/shure.yaml) and [`protocols.md`](protocols.md).

| Address | Ports | Type | Purpose |
| --- | --- | --- | --- |
| `239.255.254.253` | UDP 8427 | Multicast | Shure Discovery / multicast SLP |
| `224.0.0.251` | UDP 5353 | Multicast | mDNS (shared with Dante allowlist) |
| (unicast any) | UDP 5568 | Unicast | SDT device control |
| (unicast any) | UDP 2200–2201 | Unicast | SNET (some families) |

Do **not** seed SSDP (`239.255.255.250:1900`) unless a capture shows WWB needs it. Shure Dante cards still originate PTP/media — those stay on the deny floor.

## Lake Controller — allow (`client_vlan: control` only)

> Not loaded by either shipped profile — Lake is native to the client's own VLAN there.

Lake control rides the **Dante** VLAN (static IPv4 on the Dante subnet).

| Address | Ports | Type | Purpose | Status |
| --- | --- | --- | --- | --- |
| _TBD_ (often broadcast and/or mDNS-related) | _TBD_ | Multicast / broadcast | Device discovery | Stub — capture on Dante VLAN with Lake Controller open |
| (unicast any) | _TBD_ | Unicast | Device control | SNAT path |

Placeholder: [`config/allowlists/lake.yaml`](../config/allowlists/lake.yaml).

Until Lake groups are known, mDNS (`224.0.0.251`) reflection on Control↔Dante may help if Lake uses DNS-SD; verify with capture. Do not reflect all broadcast.

## Console control on Control (never reflected)

| Vendor | Discovery | Unicast (typical) | Combiner |
| --- | --- | --- | --- |
| Yamaha mixer-control (StageMix / Editor) | Same subnet as NETWORK / FOR MIXER CONTROL; not a published mcast allowlist | TCP 49280 (RCP) | Native L2 |
| Allen & Heath MixPad | UDP **broadcast** “find” ~1 Hz | AH-Net TCP 51321 + UDP from 51324; MIDI TCP 51325 | Native L2; broadcast cannot be reflected |
| DiGiCo **without** SoundGrid | Console control LAN | Vendor | Native L2 on Control |
| DiGiCo **with** SoundGrid | Control NIC is on the SoundGrid fabric (SoE + audio) | MultiRack on SG | **Out of scope** — do not merge onto Control |

## Management access (SSH + status page)

`management_access` in `site.yaml` picks which VLANs may reach TCP 22 and 8080. Omitted, it is Control plus Mgmt when one is configured. An explicit list is authoritative.

| Role listed | Effect |
| --- | --- |
| (omitted) | Control, plus Mgmt when configured — default posture |
| `dante` | Also accepts SSH/status from the audio VLAN. Needed only if the combiner may sit on a PVID-Dante **access** port with no tagged Control |

ICMP echo is accepted on every combiner interface regardless, so a Dante-side ping succeeding while SSH refuses is the expected signature of a Control-less port.

## ICMP / ARP

- ARP stays local to each VLAN.
- ICMP echo Control↔Dante: allow (debug).
- ICMP echo optional Mgmt↔Control / Mgmt↔Dante: allow.
- Limited broadcast (`255.255.255.255`) never kernel-forwards.
