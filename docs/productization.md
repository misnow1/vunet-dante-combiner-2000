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

MVP nftables already forwards established unicast Control→Dante, which includes Controller metering (UDP **8751**) and control ports (**4440/4444/4455/8800**). After discovery works:

1. Confirm meters in Dante Controller on Control
2. If meters fail, capture unicast ports and tighten/document in `dante_unicast_udp_ports`
3. Still **never** reflect ATP/AES67 media multicast

## Optional switch ACL hardening

Do not let Control clients bypass the combiner via a switch SVI into Dante (same rule as DHCP in [`setup.md`](setup.md): option 3 is the combiner, not an SVI). Deny Control-SVI→Dante routed traffic or omit the SVI; keep the combiner trunk; never carry SoundGrid on that trunk or the Control SSID; leave break-glass Dante access ports.

## Metering / productization status page extras (future)

- Link to break-glass doc
- Explicit “PTP drops last 5m” rate
- Firmware/version string for the combiner binary
