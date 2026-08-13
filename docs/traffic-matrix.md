# Traffic matrix

Default posture: **deny**, then allow only what control apps need. Fill VuNET/Lake rows from on-site captures ([`capture-playbook.md`](capture-playbook.md)).

## VLAN pair policy

| Src → Dst | Unicast | Multicast |
| --- | --- | --- |
| Mgmt → Control | Allow + SNAT to combiner Control IP | **Never kernel-forward** — userspace reflector, allowlisted groups only |
| Control → Mgmt | Allow (conntrack / established) | Reflect allowlisted groups only |
| Mgmt → Dante | Allow + SNAT to combiner Dante IP | **Never kernel-forward** — userspace reflector, allowlisted groups only |
| Dante → Mgmt | Allow (conntrack / established) | Reflect allowlisted groups only |
| Control ↔ Dante | **Drop** | **Drop** |
| Any → Mgmt/Control carrying PTP or media mcast | **Drop** | **Drop** |
| Any forwarded multicast (224.0.0.0/4) | **Drop** in `forward` | Reflector is sole cross-VLAN multicast path |

## Hard deny (never forward to Mgmt or Control)

| Group / range | Ports | Purpose |
| --- | --- | --- |
| `224.0.1.129`–`224.0.1.132` | UDP 319, 320 | PTP (Dante clock) — encoded as `224.0.1.128/30` + `224.0.1.132/32` |
| `239.255.0.0/16` | UDP 4321 | Dante ATP multicast audio |
| `239.69.0.0/16` | UDP 5004 | AES67 multicast audio |
| `239.254.3.3` | UDP 9998 | PTP logging (if enabled) |

These denials protect Martin amp control NICs and keep Wi-Fi/Mgmt from drowning in media.

## Dante — allow (seeded from Audinate docs)

See also [`config/allowlists/dante.yaml`](../config/allowlists/dante.yaml).

| Address | Ports | Type | Purpose |
| --- | --- | --- | --- |
| `224.0.0.251` | UDP 5353 | Multicast | mDNS / DNS-SD discovery |
| `224.0.0.230`–`224.0.0.233` | UDP 8700–8708 | Multicast | Dante control & monitoring |
| (unicast any) | UDP 4440, 4444, 4455 | Unicast | Audio control |
| (unicast any) | UDP 8751 | Unicast | Dante Controller metering |
| (unicast any) | UDP 8800 | Unicast | Control & monitoring |

Unicast rows are handled by SNAT/forwarding, not the multicast reflector. Metering (8751) is allowed in MVP nftables so a clean path to “full glass” exists; confirm in the field.

**Do not reflect** ATP/AES67 media groups or PTP.

## VuNET / Martin — allow (capture required)

| Address | Ports | Type | Purpose | Status |
| --- | --- | --- | --- | --- |
| _TBD_ | _TBD_ | Multicast | Device/controller discovery | Stub — capture on Control VLAN |
| (unicast any) | _TBD_ | Unicast | Amp control / monitoring | Allow broadly on Mgmt↔Control after capture tightens ports |

Placeholder file: [`config/allowlists/vunet.yaml`](../config/allowlists/vunet.yaml).

## Lake Controller — allow (capture required)

Lake control rides the **Dante** VLAN in this design (static IPv4 on the Dante subnet).

| Address | Ports | Type | Purpose | Status |
| --- | --- | --- | --- | --- |
| _TBD_ (often broadcast and/or mDNS-related) | _TBD_ | Multicast / broadcast | Device discovery | Stub — capture on Dante VLAN with Lake Controller open |
| (unicast any) | _TBD_ | Unicast | Device control | SNAT path |

Placeholder file: [`config/allowlists/lake.yaml`](../config/allowlists/lake.yaml).

Until Lake groups are known, mDNS (`224.0.0.251`) reflection on Dante↔Mgmt may partially help if Lake uses DNS-SD; verify with capture.

## ICMP / ARP

- ARP stays local to each VLAN (normal).
- ICMP echo Mgmt↔Control and Mgmt↔Dante: allow (helpful for debugging).
- ICMP Control↔Dante: deny.
