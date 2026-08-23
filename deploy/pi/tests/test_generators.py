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


def test_nftables_audio_trunk_dante_untagged(tmp_path: Path) -> None:
    """Default profile: Dante on the PVID (eth0), Control tagged (eth0.200)."""
    site = REPO_ROOT / "config" / "site.example.yaml"
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    # Control -> Dante crosses from the tagged VLAN out the physical NIC.
    assert 'iifname "eth0.200" oifname "eth0" ip daddr != 224.0.0.0/4 accept' in text
    assert 'oifname "eth0" ip saddr != 10.201.0.1' in text
    assert "snat to 10.201.0.1" in text
    assert "snat to 10.200.0.1" not in text
    # Denies stay aimed at Control only — never at the untagged Dante face.
    assert 'oifname { "eth0.200" } ip daddr 224.0.1.129-224.0.1.132' in text
    assert "eth0.201" not in text
    assert "drop_control_dante" not in text
    for p in DENY_MARKERS:
        assert p in text
    assert "policy drop" in text
    assert "ip daddr 224.0.0.0/4 counter name drop_forward_mcast drop" in text
    # management_access defaults to Control alone.
    assert 'iifname { "eth0.200" } tcp dport { 22, 8080 } accept' in text


def test_nftables_tagged_trunk(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.tagged-trunk.example.yaml"
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


def test_nftables_management_access_widens_input(tmp_path: Path) -> None:
    src = (REPO_ROOT / "config" / "site.example.yaml").read_text()
    site = tmp_path / "site.yaml"
    site.write_text(src.replace("# management_access: [control, dante]", "management_access: [control, dante]"))
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    assert 'iifname { "eth0.200", "eth0" } tcp dport { 22, 8080 } accept' in text


def test_nftables_rejects_two_untagged_vlans(tmp_path: Path) -> None:
    site = tmp_path / "site.yaml"
    site.write_text(
        "hostname: x\n"
        "physical_interface: eth0\n"
        "vlans:\n"
        "  control: {id: 200, address: 10.200.0.1, prefix: 21, untagged: true}\n"
        "  dante: {id: 201, address: 10.201.0.1, prefix: 21, untagged: true}\n"
        "mgmt_dhcp: {enabled: false}\n"
        "allowlist_files: []\n"
        "deny_multicast_prefixes: [224.0.1.128/30]\n"
    )
    r = _run([sys.executable, str(NFT_SCRIPT), str(site), str(tmp_path / "out.conf")])
    assert r.returncode != 0
    assert "one PVID" in r.stderr


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


def test_network_config_audio_trunk(tmp_path: Path) -> None:
    """Untagged Dante owns the parent unit; only Control gets a netdev."""
    site = REPO_ROOT / "config" / "site.example.yaml"
    out = tmp_path / "net"
    r = _run([sys.executable, str(NET_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr

    trunk = (out / "systemd/network/10-combiner-trunk.network").read_text()
    vlan_lines = [ln for ln in trunk.splitlines() if ln.startswith("VLAN=")]
    assert vlan_lines
    assert all(" " not in ln.removeprefix("VLAN=") for ln in vlan_lines)
    assert "Name=eth0" in trunk
    assert "Address=10.201.0.1/21" in trunk
    assert "Description=combiner dante (untagged/native) + trunk parent" in trunk
    assert "ConfigureWithoutCarrier=yes" in trunk
    assert "VLAN=eth0.200" in trunk
    assert "VLAN=eth0.201" not in trunk
    # No netdev for the native VLAN — its L3 lives on the parent.
    assert (out / "systemd/network/20-combiner-control.netdev").exists()
    assert not (out / "systemd/network/20-combiner-dante.netdev").exists()
    assert not (out / "systemd/network/20-combiner-mgmt.netdev").exists()
    assert (out / "combiner-mgmt-dhcp.enabled").read_text().strip() == "0"
    assert not (out / "dnsmasq.d/combiner-mgmt.conf").exists()
    ifaces = (out / "combiner-interfaces.txt").read_text()
    assert "control eth0.200" in ifaces
    assert "dante eth0\n" in ifaces
    assert "mgmt" not in ifaces


def test_network_config_tagged_trunk(tmp_path: Path) -> None:
    site = REPO_ROOT / "config" / "site.tagged-trunk.example.yaml"
    out = tmp_path / "net"
    r = _run([sys.executable, str(NET_SCRIPT), str(site), str(out)])
    assert r.returncode == 0, r.stderr

    trunk = (out / "systemd/network/10-combiner-trunk.network").read_text()
    vlan_lines = [ln for ln in trunk.splitlines() if ln.startswith("VLAN=")]
    assert vlan_lines
    assert all(" " not in ln.removeprefix("VLAN=") for ln in vlan_lines)
    assert "VLAN=eth0.200" in trunk
    assert "VLAN=eth0.201" in trunk
    assert "Address=" not in trunk
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


def test_dante_client_inverts_unicast_and_snat(site_dante_client: Path, tmp_path: Path) -> None:
    """client_vlan: dante reverses the unicast forward direction and the SNAT target.

    Dante-side clients must egress toward Control and be rewritten to the
    combiner's Control address, so Martin amps see an on-subnet peer.
    """
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site_dante_client), str(out)])
    assert r.returncode == 0, r.stderr
    text = out.read_text()

    # Unicast now flows Dante (eth0, the PVID) -> Control (eth0.200).
    assert 'iifname "eth0" oifname "eth0.200" ip daddr != 224.0.0.0/4 accept' in text
    assert 'iifname "eth0.200" oifname "eth0" ip daddr != 224.0.0.0/4 accept' not in text

    # SNAT toward Control, to the combiner's Control address.
    assert 'oifname "eth0.200" ip saddr != 10.200.0.1 counter name snat_to_control snat to 10.200.0.1' in text
    assert "counter snat_to_control {}" in text


def test_dante_client_keeps_denies_anchored_to_control(site_dante_client: Path, tmp_path: Path) -> None:
    """The PTP/media denies must NOT follow client_vlan.

    They exist to keep PTP and media off the amp VLAN. If they tracked the
    client role they would start dropping PTP toward Dante and break the clock
    of the network the combiner is meant to carry — a silent audio outage that
    looks nothing like a config error.
    """
    out = tmp_path / "nftables.conf"
    r = _run([sys.executable, str(NFT_SCRIPT), str(site_dante_client), str(out)])
    assert r.returncode == 0, r.stderr
    forward = out.read_text().split("chain forward")[1].split("chain postrouting")[0]

    for marker in DENY_MARKERS:
        deny_lines = [ln for ln in forward.splitlines() if marker in ln and "oifname" in ln]
        assert deny_lines, f"no forward deny emitted for {marker}"
        for ln in deny_lines:
            assert '"eth0.200"' in ln, f"deny for {marker} must protect Control, got: {ln.strip()}"
            # eth0 is the Dante PVID here; denying toward it would break PTP.
            assert '{ "eth0" }' not in ln, f"deny for {marker} points at Dante: {ln.strip()}"


def test_client_vlan_rejects_unknown_role(tmp_path: Path, site_example: Path) -> None:
    bad = tmp_path / "site.yaml"
    bad.write_text(site_example.read_text().replace("hostname: combiner", "hostname: combiner\nclient_vlan: mgmt"))
    r = _run([sys.executable, str(NFT_SCRIPT), str(bad), str(tmp_path / "out.conf")])
    assert r.returncode != 0
    assert "client_vlan" in (r.stderr + r.stdout)
