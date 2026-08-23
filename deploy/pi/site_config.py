"""Strict loader for combiner site.yaml, shared by the deploy generators.

The Go loader (internal/config) is authoritative and rejects unknown keys via
yaml KnownFields. These generators run from install.sh and by hand, so they
enforce the same rule: a misspelled key is an error, never a silent default.
`untaged: true` would otherwise yield a valid-looking config with the wrong
topology — tagged where the port is native, or vice versa.
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

try:
    import yaml
except ImportError:
    print("PyYAML required: pip install pyyaml / apt install python3-yaml", file=sys.stderr)
    sys.exit(1)

# Mirrors the yaml tags on config.Site / config.VLAN / config.MgmtDHCP.
SITE_KEYS = frozenset(
    {
        "hostname",
        "physical_interface",
        "status_listen",
        "client_vlan",
        "vlans",
        "mgmt_dhcp",
        "management_access",
        "allowlist_files",
        "deny_multicast_prefixes",
        "dante_unicast_udp_ports",
    }
)
VLAN_ROLES = frozenset({"mgmt", "control", "dante"})
VLAN_KEYS = frozenset({"id", "address", "prefix", "interface_name", "untagged", "gateway", "dns"})
MGMT_DHCP_KEYS = frozenset({"enabled", "range_start", "range_end", "lease", "domain"})


def _unknown(got: dict[str, Any], allowed: frozenset[str], where: str) -> list[str]:
    return [f"{where}: unknown key {k!r} (allowed: {', '.join(sorted(allowed))})" for k in sorted(set(got) - allowed)]


def load_site(path: Path | str) -> dict[str, Any]:
    """Parse site.yaml and reject unrecognized keys at every level.

    Every offending key is reported at once, so a config with several typos
    takes one round trip to fix rather than one per key.
    """
    data = yaml.safe_load(Path(path).read_text())
    if not isinstance(data, dict):
        raise SystemExit(f"site config must be a mapping: {path}")

    problems = _unknown(data, SITE_KEYS, "site.yaml")

    vlans = data.get("vlans")
    if not isinstance(vlans, dict):
        raise SystemExit("site.yaml: vlans must be a mapping")
    problems += _unknown(vlans, VLAN_ROLES, "site.yaml vlans")
    for role, v in vlans.items():
        if isinstance(v, dict):
            problems += _unknown(v, VLAN_KEYS, f"site.yaml vlans.{role}")

    dhcp = data.get("mgmt_dhcp")
    if isinstance(dhcp, dict):
        problems += _unknown(dhcp, MGMT_DHCP_KEYS, "site.yaml mgmt_dhcp")

    if problems:
        raise SystemExit("\n".join(problems))

    return data
