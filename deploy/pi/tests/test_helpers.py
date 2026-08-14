"""Unit tests for generator helpers."""

from __future__ import annotations

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


def test_merge_deny_prefixes_includes_floor(nft_mod: Any) -> None:
    out = nft_mod.merge_deny_prefixes({})
    for p in nft_mod.FLOOR_DENY_PREFIXES:
        assert nft_mod.normalize_prefix(p) in out


def test_merge_deny_prefixes_dedupes(nft_mod: Any) -> None:
    site = {"deny_multicast_prefixes": ["224.0.1.128/30", "239.255.0.0/16"]}
    out = nft_mod.merge_deny_prefixes(site)
    assert len(out) == len(set(out))
    assert out.count("224.0.1.128/30") == 1


def test_role_iface_tagged(net_mod: Any) -> None:
    assert net_mod.role_iface("control", {"id": 200}, "eth0") == "eth0.200"


def test_role_iface_untagged_mgmt(net_mod: Any) -> None:
    assert net_mod.role_iface("mgmt", {"id": 1, "untagged": True}, "eth0") == "eth0"


def test_role_iface_untagged_non_mgmt(net_mod: Any) -> None:
    with pytest.raises(SystemExit, match="only allowed on vlans.mgmt"):
        net_mod.role_iface("control", {"id": 200, "untagged": True}, "eth0")


def test_role_iface_rejects_phys_as_vlan_name(net_mod: Any) -> None:
    with pytest.raises(SystemExit, match="must differ from physical_interface"):
        net_mod.role_iface("mgmt", {"id": 209, "interface_name": "eth0"}, "eth0")
