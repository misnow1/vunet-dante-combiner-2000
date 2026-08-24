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


def test_password_placeholder_present_for_prep_card_to_substitute() -> None:
    """prep-card.sh rewrites this commented line into a real passwd: entry."""
    assert "REPLACE_WITH_PASSWORD_HASH" in USER_DATA.read_text()
    assert "REPLACE_WITH_PASSWORD_HASH" in PREP_CARD.read_text()


def test_password_auth_is_off_by_default(user_data: dict[str, Any]) -> None:
    """A card staged without --ask-password must not accept SSH passwords."""
    assert user_data["ssh_pwauth"] is False
    assert user_data["users"][0]["lock_passwd"] is True
    assert "passwd" not in user_data["users"][0]


def _prep_card(tmp_path: Path, *args: str) -> subprocess.CompletedProcess[str]:
    card = tmp_path / "card"
    card.mkdir(exist_ok=True)
    (card / "config.txt").touch()
    return subprocess.run(
        [
            str(PREP_CARD),
            "--site",
            str(REPO_ROOT / "config" / "site.example.yaml"),
            "--card",
            str(card),
            "--no-tarball",
            "--force",
            *args,
        ],
        capture_output=True,
        text=True,
    )


# A real SHA-512 crypt hash, and the short DES value macOS's crypt(3) returns
# instead when asked for one. Staging the latter would install a break-glass
# password that cannot be used.
# openssl passwd -6 -salt rtEN3GkdKSYyiLNe, for the password "testpassword".
GOOD_HASH = "$6$rtEN3GkdKSYyiLNe$4EAcEIYikXhWb2z2CfSFL0mF0QnOxXO/GyUrevJ0RKfd250DbtEiNWTpRshdwU83.dXwORKY93gT5opm62ewq."
MACOS_BROKEN_HASH = "$6HFnVrPgHUe6"


def test_password_hash_is_substituted(tmp_path: Path) -> None:
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    data = yaml.safe_load((tmp_path / "card" / "user-data").read_text())
    assert data["users"][0]["passwd"] == GOOD_HASH
    assert data["users"][0]["lock_passwd"] is False
    assert data["ssh_pwauth"] is True


@pytest.mark.parametrize(
    "bad",
    [MACOS_BROKEN_HASH, "BreakGlass2026!", "$6$short$tooshort", ""],
    ids=["macos-broken-crypt", "plaintext-password", "truncated", "empty"],
)
def test_bad_password_hash_is_refused(tmp_path: Path, bad: str) -> None:
    r = _prep_card(tmp_path, "--password-hash", bad)
    assert r.returncode != 0, f"accepted {bad!r}"


def test_short_password_is_refused(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("COMBINER_PASSWORD", "short")
    r = _prep_card(tmp_path)
    assert r.returncode != 0
    assert "at least 8 characters" in r.stderr


def test_no_key_drops_the_placeholder_instead_of_installing_it(tmp_path: Path) -> None:
    """An unreplaced placeholder would become a literal, bogus authorized key."""
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    data = yaml.safe_load((tmp_path / "card" / "user-data").read_text())
    assert "ssh_authorized_keys" not in data["users"][0]


def test_ssh_service_is_enabled_before_provisioning(user_data: dict[str, Any]) -> None:
    """Pi OS ships sshd disabled, and replacing Imager's user-data drops whatever
    it did about that. Enabling it must come FIRST, so a unit whose provisioning
    fails is still reachable — which is exactly when you need to get into it."""
    flat = [" ".join(c) if isinstance(c, list) else c for c in user_data["runcmd"]]
    assert any("ssh" in c and "systemctl enable" in c for c in flat), flat
    ssh_at = next(i for i, c in enumerate(flat) if "systemctl enable" in c)
    boot_at = next(i for i, c in enumerate(flat) if "combiner-firstboot.sh" in c)
    assert ssh_at < boot_at, "ssh must be enabled before provisioning can fail"


def test_prep_card_writes_the_ssh_marker(tmp_path: Path) -> None:
    """sshswitch.service enables sshd from this marker, covering a boot that
    never reaches runcmd."""
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    assert (tmp_path / "card" / "ssh").exists()


def test_user_option_renames_the_login(tmp_path: Path) -> None:
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH, "--user", "mpsllc")
    assert r.returncode == 0, r.stderr
    data = yaml.safe_load((tmp_path / "card" / "user-data").read_text())
    assert data["users"][0]["name"] == "mpsllc"


def test_user_option_leaves_hostname_alone(tmp_path: Path) -> None:
    """Both default to 'combiner'; only the users entry may be rewritten, since
    the real hostname comes from site.yaml when install.sh runs."""
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH, "--user", "mpsllc")
    assert r.returncode == 0, r.stderr
    data = yaml.safe_load((tmp_path / "card" / "user-data").read_text())
    assert data["hostname"] == "combiner"


@pytest.mark.parametrize(
    "bad",
    ["MPSLLC", "root:x", "1abc", "a b", 'has"quote', "x" * 33, "-lead"],
    ids=["uppercase", "colon", "leading-digit", "space", "quote", "too-long", "leading-dash"],
)
def test_bad_username_is_refused(tmp_path: Path, bad: str) -> None:
    """The name is interpolated into YAML, so a bad one could break the file as
    well as fail useradd."""
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH, "--user", bad)
    assert r.returncode != 0, f"accepted {bad!r}"
