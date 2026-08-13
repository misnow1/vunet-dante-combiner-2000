# Capture playbook

Use a dual-NIC (or dual-homed) laptop on the production network to learn exact multicast groups and ports for **VuNET** and **Lake**, and to confirm Dante matches the seeded allowlist.

## Goals

1. Produce allowlist entries for `config/allowlists/vunet.yaml` and `config/allowlists/lake.yaml`
2. Confirm Dante groups in `config/allowlists/dante.yaml`
3. Never leave a capture NIC bridging Control and Dante

## Safety

- Capture **one VLAN at a time** when possible
- Do not enable IP forwarding on the capture laptop between Control and Dante
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

Expect to see `224.0.0.251:5353` and `224.0.0.230`–`233` with ports in `8700`–`8708`. PTP (`224.0.1.129`–`132`) should **not** be added to the allowlist.

## VuNET (Martin Control VLAN)

1. Connect one NIC to Control only
2. Start capture, then launch VuNET and wait until amps appear
3. Perform a typical control action (select amp, change a non-destructive parameter if safe)

```bash
sudo tcpdump -ni eth1 -w vunet-discovery.pcap 'multicast or broadcast'
```

Extract candidates:

```bash
tshark -r vunet-discovery.pcap -T fields -e ip.src -e ip.dst -e udp.dstport -e tcp.dstport \
  | sort | uniq -c | sort -nr
```

### Critical: how does VuNET pick the unicast target?

Martin discovery multicasts are believed to **embed the device IP in the payload**. Confirm whether VuNET’s later unicast uses that embedded address or the **outer IP source** of the discovery packet.

**In the same capture (or a follow-up with VuNET actively controlling an amp):**

1. Find a discovery multicast from an amp (e.g. src `10.200.1.35` → some mcast dst).
2. Inspect the payload (Wireshark hex / “Follow UDP stream”) for `10.200.1.35` (and siblings).
3. Find the first **unicast** from the VuNET host to that amp after discovery.
4. Compare:
   - If unicast dest == **embedded payload IP** → reflector src rewrite is OK for VuNET.
   - If unicast dest tracks **discovery packet src IP** → reflecting with combiner src (`10.209.0.1`) will mis-direct VuNET; do not ship VuNET-via-combiner until we change strategy (source-preserving reflect or protocol-aware proxy).

Quick correlators:

```bash
# Multicast talkers (candidate discovery)
tshark -r vunet-discovery.pcap -Y 'ip.dst >= 224.0.0.0' -T fields \
  -e frame.time_relative -e ip.src -e ip.dst -e udp.srcport -e udp.dstport -e data

# Unicast from the VuNET PC after amps appear (set PC_IP)
tshark -r vunet-discovery.pcap -Y "ip.src == ${PC_IP} && !(ip.dst >= 224.0.0.0 and ip.dst <= 239.255.255.255)" -T fields \
  -e frame.time_relative -e ip.src -e ip.dst -e udp.dstport -e tcp.dstport
```

Add clear discovery groups to `config/allowlists/vunet.yaml` only after groups **and** the unicast-target rule above are known:

```yaml
groups:
  - name: vunet-discovery-example
    address: 239.x.x.x
    port: 1234
    proto: udp
    direction: both   # mgmt<->control
    notes: captured YYYY-MM-DD; VuNET unicast uses payload|source IP (pick one)
```

Also note unicast ports used after discovery (for optional nftables tightening later).

## Lake Controller (Dante VLAN)

1. Connect to Dante VLAN (where Lake frames live)
2. Start capture, launch Lake Controller, wait for frames to appear

```bash
sudo tcpdump -ni eth0 -w lake-discovery.pcap 'multicast or broadcast'
```

Same `tshark` summary as above. Lake may use broadcast and/or multicast; record both. Prefer allowing specific groups over reflecting all broadcast.

Update `config/allowlists/lake.yaml`.

## Feeding the combiner

After editing allowlists:

```bash
# On the combiner, reload config (service watches file or restart)
sudo systemctl restart combiner
sudo combiner-status
```

Watch the status page inventory populate as discovery traffic flows.

## What “done” looks like

| App | On Mgmt via combiner |
| --- | --- |
| Dante Controller | Devices listed; can subscribe/configure; Clock Status OK |
| VuNET | Amps listed; can control |
| Lake Controller | Frames listed; can control |

If discovery works but unicast control fails, check SNAT counters and that the combiner has the correct static IP on that VLAN.
