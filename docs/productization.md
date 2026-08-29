# Productization notes (Phase 3)

## PoE and enclosure

Lab units run on spare Raspberry Pis. Production preference:

- Small rack-tuck enclosure (1/4–1/3 depth shelf, Velcro, or short DIN only if the rack already has it)
- **PoE** preferred — most amp-rack switches already provide it
- Single trunk Ethernet to the switch; WAP remains a separate **Control** access device

Candidate Pi PoE paths: official PoE HAT / PoE+ HAT on Pi 4/5, or a PoE splitter to USB-C if HAT availability is poor.

## Sipeed RISC-V evaluation

Keep software portable (`linux/arm64` now, `linux/riscv64` later). Smoke-eval only boards with **GbE** and a Linux userspace you control.

| Board | Verdict |
| --- | --- |
| NanoKVM Cube/Lite (SG2002, ~256MB, often 100M, KVM firmware) | **Reject** as primary platform |
| NanoKVM Pro (GbE + optional PoE) | Form-factor interesting; only if stock KVM image can be replaced with normal Linux networking |
| Other Sipeed SBC with GbE + Debian/Buildroot | Best Sipeed path — add `deploy/sipeed/` profile mirroring `deploy/pi/` |

Eval checklist:

1. 802.1Q VLAN subinterfaces stable under load
2. `nftables` SNAT + named counters work
3. Multicast join/reflect on Control + Dante ifaces
4. Go `combiner` binary runs (`GOOS=linux GOARCH=riscv64`)
5. Thermal/power OK in a sealed rack niche
6. PoE actually powers the board through the intended injector/switch

## Dante metering path (“full glass”)

**Largely solved by `client_vlan: dante`, which is what both profiles now ship.** Dante Controller's metering tab and device config need L2 adjacency; SNAT carries discovery and some control but cannot supply that. Putting the client on Dante fixes it natively rather than chasing ports — which is why the shipped profile does, and why the combiner carries VuNET the other way instead.

The notes below apply only to the `client_vlan: control` alternative, where metering has to cross the combiner:

1. nftables forwards established unicast client→peer, which includes Controller metering (UDP **8751**) and control ports (**4440/4444/4455/8800**)
2. If meters fail, capture unicast ports and tighten/document in `dante_unicast_udp_ports`
3. Still **never** reflect ATP/AES67 media multicast

## Optional switch ACL hardening

Do not let clients bypass the combiner via a switch SVI into the peer VLAN (same rule as DHCP in [`setup.md`](setup.md#3-dhcp): option 3 is the combiner, not an SVI). In the shipped profile that means denying Dante-SVI→Control routed traffic, or omitting the SVI; keep the combiner trunk; never carry SoundGrid on that trunk or the client SSID; leave break-glass access ports on both VLANs.

## Metering / productization status page extras (future)

- Link to break-glass doc
- Explicit “PTP drops last 5m” rate
- Firmware/version string for the combiner binary
