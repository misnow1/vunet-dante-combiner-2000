"""Integration tests: generators against example site configs."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]
DEPLOY_PI = Path(__file__).resolve().parents[1]
NFT_SCRIPT = DEPLOY_PI / "generate-nftables.py"
NET_SCRIPT = DEPLOY_PI / "generate-network-config.py"

DENY_MARKERS = [
    "224.0.1.128/30",
    "224.0.1.132/32",
    "239.255.0.0/16",
    "239.69.0.0/16",
    "239.254.3.3/32",
]


def _run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )


@pytest.mark.parametrize(
    ("site_name", "mgmt_if", "control_if", "dante_if", "control_addr", "dante_addr"),
    [
        (
            "site.example.yaml",
            "eth0.209",
            "eth0.200",
            "eth0.201",
            "10.200.0.1",
            "10.201.0.1",
        ),
        (
            "site.lab-flat.example.yaml",
            "eth0",
            "eth0.200",
            "eth0.201",
            "10.200.0.1",
            "10.201.0.1",
        ),
    ],
)
def test_nftables_invariants(
    tmp_path: Path,
    site_name: str,
    mgmt_if: str,
    control_if: str,
    dante_if: str,
    control_addr: str,
    dante_addr: str,
) -> None:
    site = REPO_ROOT / "config" / site_name
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    assert f'iifname "{control_if}" oifname "{dante_if}"' in text
    assert f'iifname "{dante_if}" oifname "{control_if}"' in text
    assert f"snat to {control_addr}" in text
    assert f"snat to {dante_addr}" in text
    assert f'iifname "{mgmt_if}"' in text
    for p in DENY_MARKERS:
        assert p in text
    assert "policy drop" in text
    assert "ip daddr 224.0.0.0/4 counter name drop_forward_mcast drop" in text


@pytest.mark.parametrize(
    ("site_name", "untagged_mgmt", "dhcp_enabled"),
    [
        ("site.example.yaml", False, True),
        ("site.lab-flat.example.yaml", True, False),
    ],
)
def test_network_config_invariants(
    tmp_path: Path,
    site_name: str,
    untagged_mgmt: bool,
    dhcp_enabled: bool,
) -> None:
    site = REPO_ROOT / "config" / site_name
    out = tmp_path / "net"
    r = _run([sys.executable, str(NET_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr

    trunk = (out / "systemd/network/10-combiner-trunk.network").read_text()
    vlan_lines = [ln for ln in trunk.splitlines() if ln.startswith("VLAN=")]
    assert vlan_lines, "expected at least one VLAN= line"
    assert all(" " not in ln.removeprefix("VLAN=") for ln in vlan_lines)

    mgmt_netdev = out / "systemd/network/20-combiner-mgmt.netdev"
    if untagged_mgmt:
        assert not mgmt_netdev.exists()
        assert "Name=eth0" in trunk
        assert "Address=192.168.1.2/24" in trunk
        assert "Gateway=192.168.1.1" in trunk
        assert "VLAN=eth0.200" in trunk
        assert "VLAN=eth0.201" in trunk
        assert "VLAN=eth0.200 eth0.201" not in trunk
    else:
        assert mgmt_netdev.exists()
        assert "Name=eth0.209" in mgmt_netdev.read_text()
        assert "VLAN=eth0.209" in trunk
        assert "VLAN=eth0.200" in trunk
        assert "VLAN=eth0.201" in trunk

    assert (out / "systemd/network/20-combiner-control.netdev").exists()
    assert (out / "systemd/network/20-combiner-dante.netdev").exists()

    dhcp_flag = (out / "combiner-mgmt-dhcp.enabled").read_text().strip()
    assert dhcp_flag == ("1" if dhcp_enabled else "0")
    dhcp_conf = out / "dnsmasq.d/combiner-mgmt.conf"
    if dhcp_enabled:
        assert dhcp_conf.exists()
        assert "interface=eth0.209" in dhcp_conf.read_text()
    else:
        assert not dhcp_conf.exists()

    ifaces = (out / "combiner-interfaces.txt").read_text()
    assert "control eth0.200" in ifaces
    assert "dante eth0.201" in ifaces
    if untagged_mgmt:
        assert "mgmt eth0\n" in ifaces or ifaces.startswith("mgmt eth0")
    else:
        assert "mgmt eth0.209" in ifaces
