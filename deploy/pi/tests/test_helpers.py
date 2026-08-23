"""Unit tests for generator helpers."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest


def test_iface_tagged_default(nft_mod: Any) -> None:
    assert nft_mod.iface({"id": 209}, "eth0") == "eth0.209"


def test_iface_tagged_explicit(nft_mod: Any) -> None:
    assert nft_mod.iface({"id": 209, "interface_name": "eth0.209"}, "eth0") == "eth0.209"


def test_iface_untagged_uses_phys(nft_mod: Any) -> None:
    assert nft_mod.iface({"id": 1, "untagged": True}, "eth0") == "eth0"


def test_iface_untagged_mismatch(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="must equal physical_interface"):
        nft_mod.iface({"id": 1, "untagged": True, "interface_name": "eth1"}, "eth0")


def test_iface_invalid_name(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="invalid interface_name"):
        nft_mod.iface({"id": 1, "interface_name": "bad name"}, "eth0")


def test_deny_prefixes_from_site_required(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="deny_multicast_prefixes required"):
        nft_mod.deny_prefixes_from_site({})


def test_deny_prefixes_from_site_dedupes(nft_mod: Any) -> None:
    site = {"deny_multicast_prefixes": ["224.0.1.128/30", "224.0.1.128/30", "239.255.0.0/16"]}
    out = nft_mod.deny_prefixes_from_site(site)
    assert out == ["224.0.1.128/30", "239.255.0.0/16"]


def test_deny_prefixes_rejects_unicast(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="multicast"):
        nft_mod.deny_prefixes_from_site({"deny_multicast_prefixes": ["10.0.0.0/8"]})


def test_role_iface_tagged(net_mod: Any) -> None:
    assert net_mod.role_iface("control", {"id": 200}, "eth0") == "eth0.200"


def test_role_iface_untagged_mgmt(net_mod: Any) -> None:
    assert net_mod.role_iface("mgmt", {"id": 1, "untagged": True}, "eth0") == "eth0"


def test_role_iface_untagged_any_role(net_mod: Any) -> None:
    # A port has one PVID, but any one VLAN may claim it (audio trunk = dante).
    assert net_mod.role_iface("dante", {"id": 201, "untagged": True}, "eth0") == "eth0"
    assert net_mod.role_iface("control", {"id": 200, "untagged": True}, "eth0") == "eth0"


def test_role_iface_untagged_mismatch(net_mod: Any) -> None:
    with pytest.raises(SystemExit, match="must be 'eth0'"):
        net_mod.role_iface("dante", {"id": 201, "untagged": True, "interface_name": "eth1"}, "eth0")


def test_role_iface_rejects_phys_as_vlan_name(net_mod: Any) -> None:
    with pytest.raises(SystemExit, match="must differ from physical_interface"):
        net_mod.role_iface("mgmt", {"id": 209, "interface_name": "eth0"}, "eth0")


def test_management_ifaces_defaults_to_control(nft_mod: Any) -> None:
    ifs = nft_mod.management_ifaces({}, {"control": "eth0.200", "dante": "eth0"}, False)
    assert ifs == ["eth0.200"]


def test_management_ifaces_defaults_include_mgmt(nft_mod: Any) -> None:
    role_ifaces = {"control": "eth0.200", "dante": "eth0.201", "mgmt": "eth0"}
    assert nft_mod.management_ifaces({}, role_ifaces, True) == ["eth0.200", "eth0"]


def test_management_ifaces_explicit_is_authoritative(nft_mod: Any) -> None:
    role_ifaces = {"control": "eth0.200", "dante": "eth0.201", "mgmt": "eth0"}
    site = {"management_access": ["dante"]}
    assert nft_mod.management_ifaces(site, role_ifaces, True) == ["eth0.201"]


def test_management_ifaces_rejects_unknown_role(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="unknown or unconfigured role"):
        nft_mod.management_ifaces({"management_access": ["soundgrid"]}, {"control": "eth0.200"}, False)


def test_management_ifaces_rejects_unconfigured_mgmt(nft_mod: Any) -> None:
    with pytest.raises(SystemExit, match="unknown or unconfigured role"):
        nft_mod.management_ifaces({"management_access": ["mgmt"]}, {"control": "eth0.200"}, False)


def test_load_site_accepts_examples(site_config_mod: Any, site_example: Path) -> None:
    site = site_config_mod.load_site(site_example)
    assert site["physical_interface"] == "eth0"
    assert site["vlans"]["dante"]["untagged"] is True


def test_load_site_rejects_unknown_top_level_key(site_config_mod: Any, tmp_path: Path) -> None:
    p = tmp_path / "site.yaml"
    p.write_text("physical_interface: eth0\nbogus: 1\nvlans:\n  control: {id: 200}\n")
    with pytest.raises(SystemExit, match="unknown key 'bogus'"):
        site_config_mod.load_site(p)


def test_load_site_rejects_misspelled_untagged(site_config_mod: Any, tmp_path: Path) -> None:
    p = tmp_path / "site.yaml"
    p.write_text("physical_interface: eth0\nvlans:\n  dante: {id: 201, untaged: true}\n")
    with pytest.raises(SystemExit, match="vlans.dante: unknown key 'untaged'"):
        site_config_mod.load_site(p)


def test_load_site_rejects_unknown_vlan_role(site_config_mod: Any, tmp_path: Path) -> None:
    p = tmp_path / "site.yaml"
    p.write_text("physical_interface: eth0\nvlans:\n  soundgrid: {id: 300}\n")
    with pytest.raises(SystemExit, match="vlans: unknown key 'soundgrid'"):
        site_config_mod.load_site(p)


def test_load_site_reports_every_bad_key_at_once(site_config_mod: Any, tmp_path: Path) -> None:
    p = tmp_path / "site.yaml"
    p.write_text(
        "physical_interface: eth0\nbogus: 1\nvlans:\n  dante: {id: 201, untaged: true}\nmgmt_dhcp: {enabld: false}\n"
    )
    with pytest.raises(SystemExit) as e:
        site_config_mod.load_site(p)
    msg = str(e.value)
    assert "'bogus'" in msg
    assert "'untaged'" in msg
    assert "'enabld'" in msg
