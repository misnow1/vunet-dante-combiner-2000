# VuNET / Dante Control Combiner

[![CI](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/ci.yml/badge.svg)](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/ci.yml)
[![Release](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/release.yml/badge.svg)](https://github.com/misnow1/vunet-dante-combiner-2000/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/misnow1/vunet-dante-combiner-2000)](https://github.com/misnow1/vunet-dante-combiner-2000/releases/latest)

A portable Linux gateway so one control client (laptop or tablet) can run **VuNET**, mixer apps, **Dante Controller**, **Lake Controller**, and **Shure WWB** from a single NIC — while keeping amp control off Dante PTP and never attaching Waves SoundGrid.

In the shipped profile the client sits on **Dante Primary**, where Dante Controller, WWB and Lake all work natively (metering and device config need L2 adjacency, which no amount of forwarding can supply). The combiner carries **Martin VuNET** the other way, to the amps on Control.

## What it does

- Attaches to a switch **trunk** carrying **Martin Control** and **Dante** (optional lab Mgmt for the Pi uplink). Either VLAN may be the port's untagged/PVID VLAN — the shipped profile matches an **audio trunk** port (untagged Dante, tagged Control)
- Clients share the Dante broadcast domain, so Dante Controller, Lake and WWB are native. The combiner **SNATs** unicast Dante→Control so the Martin amps see an on-subnet peer
- **Reflects allowlisted multicast** discovery/control between the two VLANs — VuNET amp discovery in the shipped profile
- **Drops** PTP / multicast media toward Control; does not trunk SoundGrid

Which side the clients sit on is one setting, `client_vlan`. Both shipped profiles use `client_vlan: dante`; the `control` alternative is described in [`docs/architecture.md`](docs/architecture.md#two-profiles).

**Build a unit from a microSD card** (the normal path): [`docs/sd-image.md`](docs/sd-image.md). Stage the card at a bench, boot it on any LAN with DHCP, and it provisions itself and then **waits** — nothing is applied until you have checked it and run `combiner-go-live`, which reboots it onto its show addressing. A unit boots twice on purpose.

**Or install by hand** on a Pi you already have: [`docs/setup.md`](docs/setup.md) — addresses, switch ports, DHCP, and first checks.

## Docs

| Path | Purpose |
| --- | --- |
| [`docs/setup.md`](docs/setup.md) | **Start here** — cabling, addresses, DHCP, Pi install |
| [`docs/architecture.md`](docs/architecture.md) | Why SNAT, isolation, scope |
| [`docs/packet-flow.md`](docs/packet-flow.md) | Discovery + unicast hop-by-hop |
| [`docs/traffic-matrix.md`](docs/traffic-matrix.md) | Allow/deny groups and ports |
| [`docs/protocols.md`](docs/protocols.md) | Vendor protocol notes |
| [`docs/capture-playbook.md`](docs/capture-playbook.md) | Confirm Dante/Shure; capture Lake groups |
| [`docs/break-glass.md`](docs/break-glass.md) | Combiner down |
| [`docs/pi-prep.md`](docs/pi-prep.md) | Building binaries, Go, virgil lab board |
| [`docs/sd-image.md`](docs/sd-image.md) | Build a unit from a **microSD card** that provisions itself |
| [`docs/productization.md`](docs/productization.md) | Future hardware (PoE, Sipeed) |
| [`config/site.example.yaml`](config/site.example.yaml) | **Production** — audio trunk: clients on untagged Dante (PVID), amps on tagged Control |
| [`config/site.lab-flat.example.yaml`](config/site.lab-flat.example.yaml) | Lab: optional untagged Mgmt on a flat LAN |
| [`deploy/pi/README.md`](deploy/pi/README.md) | Installer internals and troubleshooting |
| [`cmd/combiner/`](cmd/combiner/) | Reflector + status HTTP service |
| [`cmd/combiner-status/`](cmd/combiner-status/) | CLI health snapshot |

## Lab target

**Raspberry Pi** (Debian / Raspberry Pi OS), single GbE trunk. Software is portable (static Go + systemd + YAML) for a later PoE / Sipeed GbE profile.

Maintainers: push a `v*` tag to publish packages (`git tag v0.2.4 && git push origin v0.2.4`).

## Build (developers)

```bash
go build -o bin/combiner ./cmd/combiner
go build -o bin/combiner-status ./cmd/combiner-status
```

Cross-compile:

```bash
make build-pi           # linux/arm64 — aarch64 Pi OS (lab: virgil)
make build-pi-arm       # linux/arm GOARM=7 — 32-bit Pi OS only
make build-linux-amd64  # linux/amd64
make package            # dist/*.tar.gz + SHA256SUMS (set VERSION=… as needed)
```

## CI / quality

GitHub Actions runs the same gates as `make check` (gofmt, Go tests/builds, ruff/mypy/pytest for deploy generators, `shellcheck`, `generate-check`). Pushing a `v*` tag builds release packages.

```bash
pip install -e ".[dev]"   # PyYAML + ruff, mypy, pytest
make check
```

## Status page

Once running, open `http://<control-ip>:8080/`. The combiner does not serve Control DHCP ([`docs/setup.md`](docs/setup.md)).

## Validate config before restarting

`combiner -check` is the preflight. It loads `site.yaml` (rejecting unknown keys), cross-checks allowlists against the deny floor, and reports the interfaces, management access, and reflector memberships the service would use:

```bash
combiner -check -config /etc/combiner/site.yaml
```

Exit is non-zero on any config or allowlist error, so it is safe to gate a restart on it:

```bash
combiner -check -config /etc/combiner/site.yaml && sudo systemctl restart combiner
```

Interfaces that do not exist yet are reported as a **warning**, not a failure — the same command is meant to run on a laptop, where the VLAN devices legitimately are not there. `install.sh` runs this check before it changes anything.

## Which build is this?

Release binaries are stamped, so a field unit can identify itself with no
toolchain and no network:

```bash
combiner -version          # 0.2.4
combiner-status -version
```

The version also heads `combiner -check` output, `combiner-status`, and the
status page. Unreleased builds report the git revision instead (`dev (a1b2c3d)`).

## License

[MIT](LICENSE)
