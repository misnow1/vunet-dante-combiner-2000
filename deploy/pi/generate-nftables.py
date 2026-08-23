#!/usr/bin/env python3
"""Generate nftables.conf from combiner site.yaml.

Any one VLAN may be untagged (native/PVID) — on an "audio trunk" port that is
dante. Rules key on interface names, so tagging does not change the data plane.

Safety invariants:
- forward policy drop
- unicast Control→Dante (client-initiated) + SNAT to combiner Dante IP
- optional Mgmt unicast to Control/Dante (lab uplink only)
- all multicast forwarding dropped (reflector is the sole cross-VLAN mcast path)
- PTP/media drops apply toward Control (and Mgmt if present) from any source
- IPv6 forwarding dropped
"""

from __future__ import annotations

import argparse
import ipaddress
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

from site_config import load_site

IFACE_RE = re.compile(r"^[A-Za-z0-9._-]{1,15}$")


def iface(vlan: dict[str, Any], phys: str) -> str:
    if vlan.get("untagged"):
        name = vlan.get("interface_name") or phys
        if name != phys:
            raise SystemExit(f"untagged interface_name must equal physical_interface {phys!r}, got {name!r}")
    else:
        name = vlan.get("interface_name") or f"{phys}.{vlan['id']}"
    if not IFACE_RE.match(name):
        raise SystemExit(f"invalid interface_name {name!r}")
    return name


def normalize_prefix(p: str) -> str:
    net = ipaddress.ip_network(p, strict=False)
    if not isinstance(net, ipaddress.IPv4Network):
        raise SystemExit(f"deny prefix must be IPv4: {p}")
    if not net.is_multicast:
        raise SystemExit(f"deny prefix must be multicast: {p}")
    return str(net)


def deny_prefixes_from_site(site: dict[str, Any]) -> list[str]:
    """Load deny_multicast_prefixes from site.yaml — sole source of truth for nft denies."""
    raw = site.get("deny_multicast_prefixes")
    if not raw:
        raise SystemExit("deny_multicast_prefixes required (non-empty) in site.yaml")
    if not isinstance(raw, list):
        raise SystemExit("deny_multicast_prefixes must be a list")
    seen: set[str] = set()
    out: list[str] = []
    for p in raw:
        n = normalize_prefix(str(p))
        if n not in seen:
            seen.add(n)
            out.append(n)
    return out


def validate_ipv4(addr: str, label: str) -> None:
    ip = ipaddress.ip_address(addr)
    if not isinstance(ip, ipaddress.IPv4Address):
        raise SystemExit(f"{label} must be IPv4: {addr}")


def nft_set(names: list[str]) -> str:
    return "{ " + ", ".join(f'"{n}"' for n in names) + " }"


def management_ifaces(site: dict[str, Any], role_ifaces: dict[str, str], has_mgmt: bool) -> list[str]:
    """Interfaces allowed to reach SSH/status (22, 8080).

    Omitted management_access defaults to control, plus mgmt when configured —
    the historical behavior. An explicit list is authoritative: naming
    [control, dante] on a box that also has mgmt drops SSH via mgmt.
    """
    raw = site.get("management_access")
    if raw is None:
        roles = ["control"] + (["mgmt"] if has_mgmt else [])
    elif isinstance(raw, list):
        roles = [str(r).strip().lower() for r in raw] or (["control"] + (["mgmt"] if has_mgmt else []))
    else:
        raise SystemExit("management_access must be a list")
    out: list[str] = []
    for role in roles:
        if role not in role_ifaces:
            raise SystemExit(f"management_access: unknown or unconfigured role {role!r} (want control|dante|mgmt)")
        if role_ifaces[role] not in out:
            out.append(role_ifaces[role])
    if not out:
        raise SystemExit("management_access resolved to no interfaces")
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("site_yaml")
    ap.add_argument("output", nargs="?", default="-")
    ap.add_argument(
        "--check",
        action="store_true",
        help="after write, run nft -c -f on the output (requires nft)",
    )
    args = ap.parse_args()

    site = load_site(Path(args.site_yaml))
    phys = site["physical_interface"]
    vlans = site["vlans"]
    control = vlans["control"]
    dante = vlans["dante"]
    mgmt = vlans.get("mgmt")
    has_mgmt = isinstance(mgmt, dict) and bool(mgmt.get("address"))

    validate_ipv4(control["address"], "control.address")
    validate_ipv4(dante["address"], "dante.address")
    # A switch port has exactly one PVID, so at most one VLAN may be native.
    untagged_roles = [
        role
        for role, v in (("mgmt", mgmt if has_mgmt else None), ("control", control), ("dante", dante))
        if isinstance(v, dict) and v.get("untagged")
    ]
    if len(untagged_roles) > 1:
        raise SystemExit(f"only one VLAN may be untagged (a port has one PVID): {', '.join(untagged_roles)}")

    # client_vlan decides which side the control clients sit on. It moves the
    # unicast forward direction and the SNAT target — but deliberately NOT the
    # PTP/media denies, which stay anchored to Control where the amps live.
    client_role = str(site.get("client_vlan") or "control").lower()
    if client_role not in ("control", "dante"):
        raise SystemExit(f"client_vlan: unknown role {client_role!r} (want control|dante)")
    peer_role = "control" if client_role == "dante" else "dante"
    client_label = client_role.capitalize()
    peer_label = peer_role.capitalize()

    i_control = iface(control, phys)
    i_dante = iface(dante, phys)
    i_mgmt = ""
    if has_mgmt:
        validate_ipv4(mgmt["address"], "mgmt.address")
        i_mgmt = iface(mgmt, phys)
    # SNAT is "on egress toward interface X, rewrite to X's own address", so a
    # device on X always sees an on-subnet peer. snat_to_dante is unconditional
    # (Control and any Mgmt both reach Dante through it); snat_to_control is
    # emitted when something actually egresses Control — a Mgmt uplink, or a
    # Dante-side client under client_vlan: dante.
    snat_dante = (
        f'    oifname "{i_dante}" ip saddr != {dante["address"]} '
        f"counter name snat_to_dante snat to {dante['address']}\n"
    )

    names = [i_control, i_dante]
    ids = [control["id"], dante["id"]]
    if has_mgmt:
        names.append(i_mgmt)
        ids.append(mgmt["id"])
    if len(set(names)) != len(names):
        raise SystemExit("VLAN interface names must be distinct")
    if len(set(ids)) != len(ids):
        raise SystemExit("VLAN IDs must be distinct")

    role_ifaces = {"control": i_control, "dante": i_dante}
    if has_mgmt:
        role_ifaces["mgmt"] = i_mgmt
    mgmt_access_ifs = nft_set(management_ifaces(site, role_ifaces, has_mgmt))

    deny_prefixes = deny_prefixes_from_site(site)
    all_if_names = [i_control, i_dante] + ([i_mgmt] if has_mgmt else [])
    # protected_if_names is Control (+ Mgmt) ALWAYS — never the client VLAN.
    # These denies exist to keep PTP/AES67/ATP off the amp VLAN. Letting them
    # follow client_vlan would start dropping PTP toward Dante, breaking the
    # clock of the very network it is meant to carry.
    protected_if_names = [i_control] + ([i_mgmt] if has_mgmt else [])
    i_client = i_dante if client_role == "dante" else i_control
    i_peer = i_control if client_role == "dante" else i_dante
    all_if = nft_set(all_if_names)
    protected_ifs = nft_set(protected_if_names)

    forward_denies = "\n".join(
        f'    oifname {protected_ifs} ip daddr {p} counter name drop_deny_mcast drop comment "deny mcast {p}"'
        for p in deny_prefixes
    )
    input_denies = "\n".join(f'    iifname {all_if} ip daddr {p} drop comment "input deny {p}"' for p in deny_prefixes)

    mgmt_forward = ""
    if has_mgmt:
        mgmt_forward = f"""
    # Optional lab Mgmt: unicast to Control/Dante (reflector does not join Mgmt)
    iifname "{i_mgmt}" oifname "{i_control}" ip daddr != 224.0.0.0/4 accept
    iifname "{i_control}" oifname "{i_mgmt}" ip daddr != 224.0.0.0/4 accept
    iifname "{i_mgmt}" oifname "{i_dante}" ip daddr != 224.0.0.0/4 accept
    iifname "{i_dante}" oifname "{i_mgmt}" ip daddr != 224.0.0.0/4 accept
"""

    snat_control = ""
    snat_counter = ""
    if has_mgmt or client_role == "dante":
        snat_counter = "  counter snat_to_control {}\n"
        snat_control = (
            f'    oifname "{i_control}" ip saddr != {control["address"]} '
            f"counter name snat_to_control snat to {control['address']}\n"
        )

    mgmt_input = ""
    if has_mgmt:
        mgmt_input = f"""
    iifname "{i_mgmt}" udp dport {{ 67, 68 }} accept
    iifname "{i_mgmt}" icmp type echo-request accept
"""

    text = f"""#!/usr/sbin/nft -f
# Generated by generate-nftables.py from {args.site_yaml}
# Do not edit by hand — regenerate from site.yaml
#
# Note: flush ruleset clears ALL tables on this host. Combiner Pi is dedicated.

flush ruleset

table inet combiner {{
  counter drop_ptp {{}}
  counter drop_deny_mcast {{}}
  counter drop_forward_mcast {{}}
  counter drop_invalid_path {{}}
  counter drop_ipv6_forward {{}}
{snat_counter}  counter snat_to_dante {{}}

  chain forward {{
    type filter hook forward priority filter; policy drop;

    ct state invalid drop

    # No IPv6 cross-VLAN forwarding
    meta nfproto ipv6 counter name drop_ipv6_forward drop

    # PTP (exact range) toward Control (and optional Mgmt) from any source
    oifname {protected_ifs} ip daddr 224.0.1.129-224.0.1.132 udp dport {{ 319, 320 }} counter name drop_ptp drop

    # Dante ATP media (same /16 as Shure Discovery — port 4321 only)
    oifname {protected_ifs} ip daddr 239.255.0.0/16 udp dport 4321 counter name drop_deny_mcast drop comment "ATP media"

    # Floor + site deny prefixes toward Control / Mgmt
{forward_denies}

    # Reflector is the ONLY multicast cross-VLAN path — never kernel-forward mcast
    ip daddr 224.0.0.0/4 counter name drop_forward_mcast drop
    ip daddr 255.255.255.255 counter name drop_forward_mcast drop

    ct state established,related accept

    # Unicast: {client_label} clients → {peer_label} (SNAT on egress)
    iifname "{i_client}" oifname "{i_peer}" ip daddr != 224.0.0.0/4 accept
{mgmt_forward}
    counter name drop_invalid_path drop
  }}

  chain postrouting {{
    type nat hook postrouting priority srcnat; policy accept;

{snat_control}{snat_dante}
  }}

  chain input {{
    type filter hook input priority filter; policy drop;

    ct state invalid drop
    iif lo accept

    # Drop PTP/media to the box before any multicast accept
    iifname {all_if} ip daddr 224.0.1.129-224.0.1.132 udp dport {{ 319, 320 }} counter name drop_ptp drop
    iifname {all_if} ip daddr 239.255.0.0/16 udp dport 4321 drop comment "input ATP media"
{input_denies}

    ct state established,related accept

    # Status / SSH on the management_access roles (default: Control, plus Mgmt)
    iifname {mgmt_access_ifs} tcp dport {{ 22, 8080 }} accept
    iifname "{i_control}" icmp type echo-request accept
{mgmt_input}
    # IGMP + UDP multicast for the reflector (after denies)
    meta l4proto igmp accept
    iifname {all_if} ip daddr 224.0.0.0/4 meta l4proto udp accept
    iifname {all_if} icmp type echo-request accept
  }}

  chain output {{
    type filter hook output priority filter; policy accept;
  }}
}}
"""

    if args.output == "-":
        sys.stdout.write(text)
    else:
        out = Path(args.output)
        out.write_text(text)
        print(f"Wrote {args.output}", file=sys.stderr)
        if args.check:
            r = subprocess.run(["nft", "-c", "-f", str(out)], capture_output=True, text=True)
            if r.returncode != 0:
                print(r.stderr or r.stdout, file=sys.stderr)
                return 1
            print("nft -c OK", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
