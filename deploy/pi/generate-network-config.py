#!/usr/bin/env python3
"""Generate systemd-networkd units and dnsmasq config from site.yaml."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("PyYAML required", file=sys.stderr)
    sys.exit(1)


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    print(f"wrote {path}")


def role_iface(role: str, v: dict, phys: str) -> str:
    if v.get("untagged"):
        if role != "mgmt":
            raise SystemExit("untagged is only allowed on vlans.mgmt")
        name = v.get("interface_name") or phys
        if name != phys:
            raise SystemExit(
                f"mgmt untagged interface_name must be {phys!r} (physical_interface), got {name!r}"
            )
        return phys
    name = v.get("interface_name") or f"{phys}.{v['id']}"
    if name == phys:
        # A .netdev named after the physical NIC makes networkd treat the real
        # link as a VLAN it must create, and it stops managing it entirely.
        raise SystemExit(
            f"{role}: interface_name must differ from physical_interface {phys!r}; "
            "use vlans.mgmt.untagged: true for a native/untagged Mgmt VLAN"
        )
    return name


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("site_yaml")
    ap.add_argument("outdir", help="e.g. /etc or a staging directory")
    args = ap.parse_args()

    site = yaml.safe_load(Path(args.site_yaml).read_text())
    out = Path(args.outdir)
    phys = site["physical_interface"]
    host = site.get("hostname", "combiner")
    vlans = site["vlans"]
    dhcp = site.get("mgmt_dhcp") or {}
    dhcp_enabled = dhcp.get("enabled", True)

    mgmt = vlans["mgmt"]
    mgmt_untagged = bool(mgmt.get("untagged"))
    mgmt_if = role_iface("mgmt", mgmt, phys)

    # Lab uplink only: production Mgmt is isolated and declares neither.
    mgmt_extra = ""
    if mgmt.get("gateway"):
        mgmt_extra += f"Gateway={mgmt['gateway']}\n"
    for d in mgmt.get("dns") or []:
        mgmt_extra += f"DNS={d}\n"

    tagged_ifaces: list[str] = []
    for role, v in vlans.items():
        if role == "mgmt" and v.get("untagged"):
            continue
        tagged_ifaces.append(role_iface(role, v, phys))

    # VLAN= accepts exactly one netdev per assignment; a space-separated list is
    # rejected outright ("Invalid netdev name in VLAN=") and no VLAN is created.
    vlan_lines = "".join(f"VLAN={name}\n" for name in tagged_ifaces)

    # Parent / trunk device
    if mgmt_untagged:
        # Native/untagged Mgmt on the physical NIC; Control+Dante stay tagged.
        write(
            out / "systemd/network/10-combiner-trunk.network",
            f"""[Match]
Name={phys}

[Network]
Description=combiner mgmt (untagged/native) + trunk parent
Address={mgmt['address']}/{mgmt['prefix']}
{vlan_lines}LinkLocalAddressing=no
LLMNR=no
ConfigureWithoutCarrier=yes
{mgmt_extra}""",
        )
    else:
        write(
            out / "systemd/network/10-combiner-trunk.network",
            f"""[Match]
Name={phys}

[Network]
{vlan_lines}LinkLocalAddressing=no
LLMNR=no
""",
        )

    for role, v in vlans.items():
        if role == "mgmt" and v.get("untagged"):
            continue  # L3 lives on the parent unit above
        ifname = role_iface(role, v, phys)
        write(
            out / f"systemd/network/20-combiner-{role}.netdev",
            f"""[NetDev]
Name={ifname}
Kind=vlan

[VLAN]
Id={v['id']}
""",
        )
        write(
            out / f"systemd/network/20-combiner-{role}.network",
            f"""[Match]
Name={ifname}

[Network]
Description=combiner {role} VLAN {v['id']}
Address={v['address']}/{v['prefix']}
LinkLocalAddressing=no
ConfigureWithoutCarrier=yes
{mgmt_extra if role == "mgmt" else ""}""",
        )

    # Markers consumed by install.sh
    write(out / "combiner-mgmt-dhcp.enabled", "1\n" if dhcp_enabled else "0\n")
    write(
        out / "combiner-interfaces.txt",
        "".join(f"{role} {role_iface(role, v, phys)}\n" for role, v in vlans.items()),
    )

    if dhcp_enabled:
        domain = dhcp.get("domain", "combiner.local")
        for key in ("range_start", "range_end", "lease"):
            if key not in dhcp:
                raise SystemExit(f"mgmt_dhcp.{key} required when mgmt_dhcp.enabled is true")
        write(
            out / "dnsmasq.d/combiner-mgmt.conf",
            f"""# Combiner Mgmt DHCP — do not enable DHCP on Control/Dante
# No DNS on Mgmt by design (option 6 empty) — use combiner Mgmt IP for status page.
interface={mgmt_if}
bind-interfaces
domain={domain}
dhcp-range={dhcp['range_start']},{dhcp['range_end']},{dhcp['lease']}
dhcp-option=option:router,{mgmt['address']}
dhcp-option=option:dns-server
no-resolv
bogus-priv
domain-needed
""",
        )

    write(
        out / "hostname.combiner",
        f"{host}\n",
    )

    # Forwarding is enabled by install.sh ONLY after nftables is validated+loaded.
    write(
        out / "sysctl.d/99-combiner-redirects.conf",
        """# Do not send ICMP redirects that might encourage asymmetric paths
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
""",
    )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
