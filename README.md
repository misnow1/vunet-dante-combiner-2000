# VuNET / Dante Control Combiner

[![CI](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/ci.yml/badge.svg)](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/ci.yml)
[![Release](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/release.yml/badge.svg)](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/misnow1/vunet-dante-combiner-2000)](https://github.com/misnow1/vunet-dante-combiner-2000/releases/latest)

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
| [`docs/pi-prep.md`](docs/pi-prep.md) | Pi packages, release downloads, Go toolchain, arm vs arm64 |
| [`config/site.example.yaml`](config/site.example.yaml) | Site configuration example |
| [`config/site.lab-flat.example.yaml`](config/site.lab-flat.example.yaml) | Lab config: untagged Mgmt on an existing flat LAN |
| [`deploy/pi/`](deploy/pi/) | Raspberry Pi lab install profile |
| [`cmd/combiner/`](cmd/combiner/) | Reflector + status HTTP service |
| [`cmd/combiner-status/`](cmd/combiner-status/) | CLI health snapshot |

## Lab target

**Raspberry Pi** (Debian / Raspberry Pi OS), single GbE trunk. Software is portable (static Go + systemd + YAML) for a later PoE / Sipeed GbE profile.

## Install (Pi) — preferred

Download a release tarball (no Go on the Pi):

1. Pick the asset matching `uname -m` from [Releases](https://github.com/misnow1/vunet-dante-combiner-2000/releases): `linux-arm64` (lab), `linux-arm`, or `linux-amd64`
2. Extract, copy/edit `site.yaml`, run `deploy/pi/install.sh`

Details: [`docs/pi-prep.md`](docs/pi-prep.md) and [`deploy/pi/README.md`](deploy/pi/README.md).

Maintainers: push a `v*` tag to publish packages (`git tag v0.1.0 && git push origin v0.1.0`).

## Build (developers)

```bash
go build -o bin/combiner ./cmd/combiner
go build -o bin/combiner-status ./cmd/combiner-status
```

Cross-compile:

```bash
make build-pi           # linux/arm64 — aarch64 Pi OS (lab: virgil01)
make build-pi-arm       # linux/arm GOARM=7 — 32-bit Pi OS only
make build-linux-amd64  # linux/amd64
make package            # dist/*.tar.gz + SHA256SUMS (set VERSION=… as needed)
```

## CI / quality

GitHub Actions runs the same gates as `make check` (gofmt, Go tests/builds, ruff/mypy/pytest for deploy generators, `generate-check`). Pushing a `v*` tag builds release packages.

```bash
pip install -e ".[dev]"   # PyYAML + ruff, mypy, pytest
make check
```

## Status page

Once running, open `http://<mgmt-ip>:8080/`. Mgmt DHCP does not provide DNS.

Validate config without starting:

```bash
./bin/combiner -check -config config/site.example.yaml
```

## License

[MIT](LICENSE)
