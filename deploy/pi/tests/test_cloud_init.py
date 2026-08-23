"""The cloud-init assets staged onto a card's FAT boot partition.

These files are only exercised for real on a Pi's first boot, where a mistake
costs a card re-flash and a trip to the rack. Parse and shape them here instead.
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Any

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[3]
CLOUD_INIT = REPO_ROOT / "deploy" / "pi" / "cloud-init"
USER_DATA = CLOUD_INIT / "user-data"
META_DATA = CLOUD_INIT / "meta-data"
FIRSTBOOT = CLOUD_INIT / "combiner-firstboot.sh"
PREP_CARD = REPO_ROOT / "deploy" / "pi" / "prep-card.sh"

# prep-card.sh substitutes this; renaming it in one file and not the other would
# silently ship cards that nothing can log into.
SSH_PLACEHOLDER = "REPLACE_WITH_YOUR_SSH_PUBLIC_KEY"


@pytest.fixture(scope="module")
def user_data() -> dict[str, Any]:
    data = yaml.safe_load(USER_DATA.read_text())
    assert isinstance(data, dict)
    return data


def test_user_data_starts_with_cloud_config_header() -> None:
    # cloud-init ignores a user-data file whose first line is not this.
    assert USER_DATA.read_text().splitlines()[0] == "#cloud-config"


def test_user_data_is_valid_yaml(user_data: dict[str, Any]) -> None:
    assert isinstance(user_data, dict)


def test_meta_data_is_valid_yaml() -> None:
    meta = yaml.safe_load(META_DATA.read_text())
    # NoCloud needs instance-id present to treat the partition as a datasource.
    assert "instance-id" in meta


def test_runcmd_invokes_the_firstboot_script(user_data: dict[str, Any]) -> None:
    runcmd = user_data["runcmd"]
    flat = [" ".join(c) if isinstance(c, list) else c for c in runcmd]
    assert any("combiner-firstboot.sh" in c for c in flat), flat


def test_firstboot_path_matches_where_prep_card_writes_it(user_data: dict[str, Any]) -> None:
    """runcmd hardcodes a path; prep-card.sh decides the filename. Keep them equal."""
    flat = [" ".join(c) if isinstance(c, list) else c for c in user_data["runcmd"]]
    invoked = next(c for c in flat if "combiner-firstboot.sh" in c)
    assert "/boot/firmware/combiner-firstboot.sh" in invoked
    assert 'combiner-firstboot.sh"' in PREP_CARD.read_text()


def test_runtime_packages_are_declared(user_data: dict[str, Any]) -> None:
    """Installed while the bench network is up, before install.sh rewrites it."""
    packages = set(user_data["packages"])
    assert {"nftables", "python3-yaml", "iproute2", "conntrack"} <= packages


def test_a_login_is_defined(user_data: dict[str, Any]) -> None:
    users = user_data["users"]
    assert users, "replacing Imager's user-data drops its account; define one here"
    assert users[0]["name"]


def test_ssh_placeholder_present_for_prep_card_to_substitute() -> None:
    assert SSH_PLACEHOLDER in USER_DATA.read_text()
    assert SSH_PLACEHOLDER in PREP_CARD.read_text()


def test_site_config_filename_agrees_between_script_and_prep_card() -> None:
    assert "combiner-site.yaml" in FIRSTBOOT.read_text()
    assert "combiner-site.yaml" in PREP_CARD.read_text()


@pytest.mark.parametrize("script", [FIRSTBOOT, PREP_CARD])
def test_shell_scripts_parse(script: Path) -> None:
    assert script.stat().st_mode & 0o111, f"{script.name} is not executable"
    subprocess.run(["bash", "-n", str(script)], check=True)
