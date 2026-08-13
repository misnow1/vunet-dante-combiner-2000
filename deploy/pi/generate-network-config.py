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
    dhcp = site["mgmt_dhcp"]

    vlan_ifaces = []
    for role, v in vlans.items():
        ifname = v.get("interface_name") or f"{phys}.{v['id']}"
        vlan_ifaces.append(ifname)

    # Parent device — do not DHCP the trunk itself
    write(
        out / "systemd/network/10-combiner-trunk.network",
        f"""[Match]
Name={phys}

[Network]
VLAN={' '.join(vlan_ifaces)}
LinkLocalAddressing=no
LLMNR=no
""",
    )

    for role, v in vlans.items():
        ifname = v.get("interface_name") or f"{phys}.{v['id']}"
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
IPForward=yes
LinkLocalAddressing=no
ConfigureWithoutCarrier=yes
""",
        )

    mgmt_if = vlans["mgmt"].get("interface_name") or f"{phys}.{vlans['mgmt']['id']}"
    domain = dhcp.get("domain", "combiner.local")
    write(
        out / "dnsmasq.d/combiner-mgmt.conf",
        f"""# Combiner Mgmt DHCP — do not enable DHCP on Control/Dante
# No DNS on Mgmt by design (option 6 empty) — use combiner Mgmt IP for status page.
interface={mgmt_if}
bind-interfaces
domain={domain}
dhcp-range={dhcp['range_start']},{dhcp['range_end']},{dhcp['lease']}
dhcp-option=option:router,{vlans['mgmt']['address']}
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
