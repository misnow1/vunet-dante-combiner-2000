# VuNET / Dante Control Combiner

A portable Linux Mgmt-VLAN gateway so one control client (laptop or tablet) can run **VuNET**, **Dante Controller**, and **Lake Controller** without dual NICs — while keeping Martin Control off Dante PTP.

## What it does

- Attaches to a switch **trunk** with three VLANs: **Mgmt**, **Martin Control**, **Dante**
- Runs DHCP on **Mgmt only** and advertises itself as the default gateway
- **SNATs** unicast so Control/Dante devices see an on-subnet peer (amps have no useful default route)
- **Reflects allowlisted multicast** discovery/control between Mgmt↔Control and Mgmt↔Dante
- **Hard-isolates** Control↔Dante and drops PTP / multicast media toward Mgmt and Control

## Quick map

| Path | Purpose |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | Topology, SNAT rationale, isolation rules |
| [`docs/packet-flow.md`](docs/packet-flow.md) | Discovery + unicast flows with production IPs |
| [`docs/traffic-matrix.md`](docs/traffic-matrix.md) | Allow/deny groups and ports |
| [`docs/capture-playbook.md`](docs/capture-playbook.md) | How to capture VuNET/Lake groups on site |
| [`docs/break-glass.md`](docs/break-glass.md) | What to do if the combiner dies |
| [`docs/productization.md`](docs/productization.md) | PoE, enclosure, Sipeed eval, metering, ACLs |
| [`config/site.example.yaml`](config/site.example.yaml) | Site configuration example |
| [`deploy/pi/`](deploy/pi/) | Raspberry Pi lab install profile |
| [`cmd/combiner/`](cmd/combiner/) | Reflector + status HTTP service |
| [`cmd/combiner-status/`](cmd/combiner-status/) | CLI health snapshot |

## Lab target

**Raspberry Pi** (Debian / Raspberry Pi OS), single GbE trunk. Software is portable (static Go + systemd + YAML) for a later PoE / Sipeed GbE profile.

## Build

```bash
go build -o bin/combiner ./cmd/combiner
go build -o bin/combiner-status ./cmd/combiner-status
```

Cross-compile for Pi (`linux/arm64`):

```bash
GOOS=linux GOARCH=arm64 go build -o bin/combiner-linux-arm64 ./cmd/combiner
GOOS=linux GOARCH=arm64 go build -o bin/combiner-status-linux-arm64 ./cmd/combiner-status
```

## Install (Pi)

See [`deploy/pi/README.md`](deploy/pi/README.md).

## Status page

Once running, open `http://<mgmt-ip>:8080/`. Mgmt DHCP does not provide DNS.

Validate config without starting:

```bash
./bin/combiner -check -config config/site.example.yaml
```
