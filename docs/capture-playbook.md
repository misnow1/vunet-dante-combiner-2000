# Capture playbook

Use a dual-NIC (or dual-homed) laptop on the production network to confirm Dante and Shure allowlists, fill **Lake** groups, and optionally document VuNET. Clients already share Control with VuNET / Yamaha / MixPad — those protocols are **not** reflector inputs.

Background: [`protocols.md`](protocols.md). Allow/deny: [`traffic-matrix.md`](traffic-matrix.md).

## Goals

1. Confirm Dante groups in `config/allowlists/dante.yaml`
2. Confirm Shure Discovery `239.255.254.253:8427` (and that PTP is absent from allowlists)
3. Produce allowlist entries for `config/allowlists/lake.yaml`
4. Optional: document VuNET talkers for operators (do **not** load them into the reflector)
5. Never leave a capture NIC bridging Control, Dante, or SoundGrid

## Safety

- Capture **one VLAN at a time** when possible
- Do not enable IP forwarding on the capture laptop between Control and Dante
- **Do not** span or sit on a SoundGrid switch for “completeness” — that fabric carries SoE clock and audio
- Prefer a span/mirror port or a filtered access port over sitting in the middle of amp control during a show

## Tools

```bash
sudo apt install tcpdump tshark   # Debian/Ubuntu
# or
brew install wireshark            # macOS — includes tshark
```

## Dante confirmation (Dante VLAN)

With Dante Controller open and devices online:

```bash
sudo tcpdump -ni eth0 -w dante-discovery.pcap \
  'multicast and not net 239.255.0.0/16 and not net 239.69.0.0/16'
```

Summarize talkers:

```bash
tshark -r dante-discovery.pcap -q -z endpoints,ip
tshark -r dante-discovery.pcap -T fields -e ip.dst -e udp.dstport \
  | sort | uniq -c | sort -nr | head -50
```

Expect `224.0.0.251:5353` and `224.0.0.230`–`233` with ports in `8700`–`8708`. PTP (`224.0.1.129`–`132`) must **not** be added to any allowlist.

## Shure Wireless Workbench (Dante VLAN)

Same NIC as Dante. With WWB open and receivers online:

```bash
sudo tcpdump -ni eth0 -w shure-wwb.pcap \
  'udp port 8427 or (udp port 5353 and multicast)'
```

Expect `239.255.254.253` UDP **8427**. mDNS `224.0.0.251:5353` is already covered by the Dante allowlist. If you see SSDP `239.255.255.250:1900` and WWB still discovers without it, leave SSDP off the allowlist.

Unicast after discovery is typically UDP **5568** (and sometimes **2200–2201**); those use SNAT, not the reflector.

## VuNET (Martin Control VLAN) — documentation only

VuNET is native L2 on Control. Capture only if you want a record of groups/ports; **do not** add them to combiner allowlists (`vlan` must be `dante`).

1. Connect one NIC to Control only (same VLAN as the WAP/clients)
2. Launch VuNET, wait until amps appear, perform a safe control action

```bash
sudo tcpdump -ni eth1 -w vunet-discovery.pcap 'multicast or broadcast'
```

```bash
tshark -r vunet-discovery.pcap -T fields -e ip.src -e ip.dst -e udp.dstport -e tcp.dstport \
  | sort | uniq -c | sort -nr
```

## Lake Controller (Dante VLAN)

1. Connect to Dante VLAN (where Lake frames live)
2. Start capture, launch Lake Controller, wait for frames to appear

```bash
sudo tcpdump -ni eth0 -w lake-discovery.pcap 'multicast or broadcast'
```

Lake may use broadcast and/or multicast; record both. Prefer specific multicast groups over reflecting all broadcast. Update `config/allowlists/lake.yaml` (`vlan: dante`).

## Yamaha / Allen & Heath / DiGiCo

No combiner allowlist. Confirm on Control that StageMix/Editor and MixPad discover without the combiner (they should). MixPad uses UDP **broadcast**; if it fails, the WAP or switch is filtering broadcasts — not a reflector bug.

DiGiCo **with Waves SoundGrid**: capture on the SG switch is out of scope. Do not merge that VLAN onto Control.

## Feeding the combiner

After editing Dante/Shure/Lake allowlists:

```bash
sudo systemctl restart combiner
sudo combiner-status
```

Watch the status page inventory populate as **Dante-side** discovery is reflected onto Control.

## What “done” looks like

| App | On Control |
| --- | --- |
| VuNET | Native — amps listed; can control (no reflector) |
| Yamaha StageMix / Editor | Native — console listed or IP entered |
| A&H MixPad | Native — mixer found via broadcast |
| Dante Controller | Devices listed via reflector; Clock Status OK (PTP stays on Dante) |
| Wireless Workbench | Receivers listed; unicast control works (SNAT) |
| Lake Controller | Frames listed after Lake groups are allowlisted; can control |

If Dante/WWB/Lake discovery works but unicast control fails, check `snat_to_dante` and that the combiner has the correct static IP on Dante. Control clients must use the **combiner Control IP as default gateway** ([`setup.md`](setup.md)) so Dante destinations are routed through SNAT — not a switch SVI.
