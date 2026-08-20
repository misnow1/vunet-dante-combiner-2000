"""Integration tests: generators against example site configs."""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
DEPLOY_PI = Path(__file__).resolve().parents[1]
NFT_SCRIPT = DEPLOY_PI / "generate-nftables.py"
NET_SCRIPT = DEPLOY_PI / "generate-network-config.py"

DENY_MARKERS = [
    "224.0.1.128/30",
    "224.0.1.132/32",
    "239.69.0.0/16",
    "239.254.3.3/32",
    "udp dport 4321",
]


def _run(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        cmd,
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=False,
    )


def test_nftables_production_control_dante(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.example.yaml"
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    assert 'iifname "eth0.200" oifname "eth0.201" ip daddr != 224.0.0.0/4 accept' in text
    assert "drop_control_dante" not in text
    assert "snat to 10.201.0.1" in text
    assert "snat to 10.200.0.1" not in text
    assert "eth0.209" not in text
    for p in DENY_MARKERS:
        assert p in text
    assert "policy drop" in text
    assert "ip daddr 224.0.0.0/4 counter name drop_forward_mcast drop" in text
    assert "tcp dport { 22, 8080 }" in text


def test_nftables_lab_flat_optional_mgmt(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.lab-flat.example.yaml"
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    assert 'iifname "eth0.200" oifname "eth0.201"' in text
    assert 'iifname "eth0" oifname "eth0.200"' in text
    assert "snat to 10.200.0.1" in text
    assert "snat to 10.201.0.1" in text
    for p in DENY_MARKERS:
        assert p in text


def test_network_config_production(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.example.yaml"
    out = tmp_path / "net"
    r = _run([sys.executable, str(NET_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr

    trunk = (out / "systemd/network/10-combiner-trunk.network").read_text()
    vlan_lines = [ln for ln in trunk.splitlines() if ln.startswith("VLAN=")]
    assert vlan_lines
    assert all(" " not in ln.removeprefix("VLAN=") for ln in vlan_lines)
    assert "VLAN=eth0.200" in trunk
    assert "VLAN=eth0.201" in trunk
    assert not (out / "systemd/network/20-combiner-mgmt.netdev").exists()
    assert (out / "systemd/network/20-combiner-control.netdev").exists()
    assert (out / "systemd/network/20-combiner-dante.netdev").exists()
    assert (out / "combiner-mgmt-dhcp.enabled").read_text().strip() == "0"
    assert not (out / "dnsmasq.d/combiner-mgmt.conf").exists()
    ifaces = (out / "combiner-interfaces.txt").read_text()
    assert "control eth0.200" in ifaces
    assert "dante eth0.201" in ifaces
    assert "mgmt" not in ifaces


def test_network_config_lab_flat(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.lab-flat.example.yaml"
    out = tmp_path / "net"
    r = _run([sys.executable, str(NET_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr

    trunk = (out / "systemd/network/10-combiner-trunk.network").read_text()
    assert not (out / "systemd/network/20-combiner-mgmt.netdev").exists()
    assert "Name=eth0" in trunk
    assert "Address=192.168.1.2/24" in trunk
    assert "Gateway=192.168.1.1" in trunk
    assert "VLAN=eth0.200" in trunk
    assert "VLAN=eth0.201" in trunk
    assert "VLAN=eth0.200 eth0.201" not in trunk
    assert (out / "combiner-mgmt-dhcp.enabled").read_text().strip() == "0"
    ifaces = (out / "combiner-interfaces.txt").read_text()
    assert "mgmt eth0" in ifaces
    assert "control eth0.200" in ifaces
