"""The cloud-init assets staged onto a card's FAT boot partition.

These files are only exercised for real on a Pi's first boot, where a mistake
costs a card re-flash and a trip to the rack. Parse and shape them here instead.
"""

from __future__ import annotations

import re
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
APPLY = REPO_ROOT / "deploy" / "pi" / "combiner-apply.sh"
SEAL = REPO_ROOT / "deploy" / "pi" / "combiner-seal.sh"
INSTALL = REPO_ROOT / "deploy" / "pi" / "install.sh"
SYSTEMD = REPO_ROOT / "deploy" / "pi" / "systemd"

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


@pytest.mark.parametrize("script", [FIRSTBOOT, PREP_CARD, APPLY, INSTALL, SEAL])
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

    # ssh.service fails its own `sshd -t` when host keys are missing, so
    # enabling without generating them first achieves nothing on a sealed or
    # key-less card. Generation must come first, in the same step.
    step = flat[ssh_at]
    assert "ssh-keygen -A" in step, step
    assert step.index("ssh-keygen -A") < step.index("systemctl enable"), step


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


def test_staging_overwrites_a_stale_instance_id(tmp_path: Path) -> None:
    """cloud-init runs users/runcmd once per instance-id. Keeping Imager's fixed
    id makes re-staging an already-booted card silently inert — no provisioning,
    no log — so every stage must claim a new instance."""
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    (card / "meta-data").write_text("instance-id: rpi-imager-1787532598498\n")

    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    meta = yaml.safe_load((card / "meta-data").read_text())
    assert meta["instance-id"] != "rpi-imager-1787532598498"
    assert meta["instance-id"].startswith("combiner-")


def test_each_stage_claims_a_new_instance(tmp_path: Path) -> None:
    first = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert first.returncode == 0, first.stderr
    one = yaml.safe_load((tmp_path / "card" / "meta-data").read_text())["instance-id"]
    second = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert second.returncode == 0, second.stderr
    two = yaml.safe_load((tmp_path / "card" / "meta-data").read_text())["instance-id"]
    assert one != two


def test_previous_boot_log_is_kept_not_deleted(tmp_path: Path) -> None:
    """Staging must not destroy the log from a boot you are still debugging,
    while still keeping a previous unit's log from being read as this one's."""
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    (card / "combiner-firstboot.log").write_text("evidence from the failed boot\n")

    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    assert not (card / "combiner-firstboot.log").exists()
    assert (card / "combiner-firstboot.log.prev").read_text() == "evidence from the failed boot\n"


def test_install_delegates_the_config_half_to_combiner_apply() -> None:
    """install.sh keeps one-time OS prep; everything config-derived lives in
    combiner-apply so an install and a later re-home run the same code path."""
    text = INSTALL.read_text()
    assert "combiner-apply" in text
    # install.sh still INSTALLS the generators to a stable path; it must no
    # longer RUN them, nor touch the ruleset itself.
    assert not re.search(r"^\s*python3 .*generate-", text, re.M), "install.sh still runs a generator"
    for moved in ("nft flush ruleset", "nft -c -f", "hostnamectl set-hostname"):
        assert moved not in text, f"{moved} should have moved into combiner-apply"


def test_apply_service_runs_before_combiner() -> None:
    """combiner must not start against a config that has not been applied yet."""
    unit = (SYSTEMD / "combiner-apply.service").read_text()
    assert "Before=combiner.service" in unit
    assert "Type=oneshot" in unit
    assert "combiner-apply.service" in (SYSTEMD / "combiner.service").read_text()


def test_apply_restarts_combiner_without_blocking() -> None:
    """A blocking restart of a unit ordered after us deadlocks the boot."""
    text = APPLY.read_text()
    assert "restart --no-block combiner" in text


def test_apply_normalises_the_generated_ruleset_header() -> None:
    """generate-nftables.py stamps its input path into a header comment. Without
    normalising it, the staged temp path differs every run and apply would fire
    on every boot."""
    assert "Generated by generate-nftables.py from" in APPLY.read_text()


def test_seal_keeps_what_makes_a_clone_worth_cloning() -> None:
    """A clone must NOT re-provision: apt needs a mirror and a rack has none.
    So cloud-init's record that provisioning happened has to survive sealing."""
    text = SEAL.read_text()
    assert "cloud-init clean" not in text, "sealing must not reset cloud-init"
    assert "rm -f /var/lib/combiner/provisioned" not in text
    assert "rm -rf /var/lib/combiner" not in text


def test_seal_clears_every_shared_identity() -> None:
    """Anything a dd-clone would duplicate across the fleet."""
    text = SEAL.read_text()
    assert "truncate -s 0 /etc/machine-id" in text
    assert "rm -f /etc/ssh/ssh_host_*" in text
    assert "random-seed" in text


def test_seal_forces_each_clone_to_carry_its_own_config() -> None:
    """Leaving the golden site.yaml in /etc would let a clone whose card is
    missing one come up silently on the WRONG addressing."""
    assert "rm -f /etc/combiner/site.yaml" in SEAL.read_text()


def test_seal_installs_a_host_key_safety_net() -> None:
    """Pi OS gates host-key regeneration on ConditionFirstBoot, which was
    observed NOT to fire on a sealed card — leaving a unit with no host keys, and
    ssh.service fails its own `sshd -t` check when keys are missing. So sealing
    installs an unconditional generator of its own."""
    text = SEAL.read_text()
    assert "combiner-hostkeys.service" in text
    assert "ExecStart=/usr/bin/ssh-keygen -A" in text
    assert "Before=ssh.service" in text


def test_seal_does_not_use_an_ssh_service_dropin() -> None:
    """A drop-in cannot work here: drop-in ExecStartPre= lines are APPENDED, so
    they run after ssh.service's own `sshd -t`, which has already failed. Sealing
    must remove any such drop-in, never add one."""
    text = SEAL.read_text()
    assert "rm -f /etc/systemd/system/ssh.service.d/10-combiner-hostkeys.conf" in text
    assert "ExecStartPre=-/usr/bin/ssh-keygen" not in text
    # [Unit] dependencies DO merge additively, so a drop-in is the right tool
    # for making ssh pull the generator in — just not for ExecStartPre.
    assert "Wants=combiner-hostkeys.service" in text
    assert "After=combiner-hostkeys.service" in text


def test_seal_refuses_to_run_unconfirmed() -> None:
    r = subprocess.run([str(SEAL), "--dry-run", "--help"], capture_output=True, text=True)
    assert r.returncode == 0
    assert "--yes" in r.stdout


REAL_CMDLINE = (
    "console=serial0,115200 console=tty1 root=PARTUUID=9710ec8c-02 rootfstype=ext4 "
    "fsck.repair=yes rootwait quiet splash plymouth.ignore-serial-consoles "
    "cfg80211.ieee80211_regdom=US ds=nocloud;i=rpi-imager-1787532598498\n"
)


def test_staging_repins_the_instance_id_on_the_kernel_cmdline(tmp_path: Path) -> None:
    """Imager pins the instance-id as `ds=nocloud;i=<id>` on the kernel command
    line, and that beats meta-data on the seed. Stamping meta-data alone is
    silently ineffective — cloud-init keeps seeing the original instance and
    never re-runs users or runcmd, so a re-staged card provisions nothing."""
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    (card / "cmdline.txt").write_text(REAL_CMDLINE)

    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr

    cmdline = (card / "cmdline.txt").read_text()
    meta_id = yaml.safe_load((card / "meta-data").read_text())["instance-id"]
    assert f"i={meta_id}" in cmdline, cmdline
    assert "rpi-imager-1787532598498" not in cmdline
    # A multi-line or mangled cmdline.txt makes the Pi unbootable.
    assert cmdline.count("\n") <= 1
    assert "root=PARTUUID=9710ec8c-02" in cmdline
    assert "rootfstype=ext4" in cmdline
    assert (card / "cmdline.txt.combiner-orig").exists()


def test_staging_leaves_a_cmdline_without_nocloud_alone(tmp_path: Path) -> None:
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    plain = "console=tty1 root=PARTUUID=abc-02 rootwait\n"
    (card / "cmdline.txt").write_text(plain)
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    assert (card / "cmdline.txt").read_text() == plain


def test_cmdline_rewrite_only_touches_the_nocloud_parameter(tmp_path: Path) -> None:
    """A cmdline carries other parameters that END in i=, e.g.
    snd_bcm2835.enable_hdmi=1 contains the substring "i=1". Matching the first
    bare i= would corrupt that parameter AND leave the real instance-id alone —
    producing an unbootable Pi that also failed to re-provision."""
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    (card / "cmdline.txt").write_text(
        "console=tty1 snd_bcm2835.enable_hdmi=1 root=PARTUUID=abc-02 ds=nocloud;i=rpi-imager-123\n"
    )
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr

    cmdline = (card / "cmdline.txt").read_text()
    meta_id = yaml.safe_load((card / "meta-data").read_text())["instance-id"]
    assert "snd_bcm2835.enable_hdmi=1" in cmdline, cmdline
    assert f"ds=nocloud;i={meta_id}" in cmdline, cmdline
    assert "rpi-imager-123" not in cmdline


def test_cmdline_rewrite_preserves_other_nocloud_options(tmp_path: Path) -> None:
    card = tmp_path / "card"
    card.mkdir()
    (card / "config.txt").touch()
    (card / "cmdline.txt").write_text("root=PARTUUID=abc-02 ds=nocloud;s=/boot/firmware/;i=rpi-imager-123 quiet\n")
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    cmdline = (card / "cmdline.txt").read_text()
    assert "s=/boot/firmware/" in cmdline, cmdline
    assert cmdline.rstrip().endswith("quiet"), cmdline


def _stage_with_config(tmp_path: Path, config_txt: str) -> Path:
    card = tmp_path / "card"
    card.mkdir(exist_ok=True)
    (card / "config.txt").write_text(config_txt)
    (card / "cmdline.txt").write_text("console=serial0,115200 root=PARTUUID=abc-02 ds=nocloud;i=rpi-imager-1\n")
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    return card


def test_staging_enables_the_serial_console(tmp_path: Path) -> None:
    """A racked unit with no console can only be debugged by pulling its card.
    Set at stage time, not during provisioning, so it works on the very first
    boot — including one where provisioning fails, which is when it is needed."""
    card = _stage_with_config(tmp_path, "arm_64bit=1\n")
    assert "enable_uart=1" in (card / "config.txt").read_text()


def test_enable_uart_is_not_trapped_in_a_model_section(tmp_path: Path) -> None:
    """config.txt uses conditional filters. Appending bare to a file ending in
    [pi5] would silently apply enable_uart to one model only."""
    card = _stage_with_config(tmp_path, "arm_64bit=1\n\n[pi5]\ndtoverlay=nospi10\n")
    text = card / "config.txt"
    body = text.read_text()
    after_uart = body[: body.index("enable_uart=1")]
    # The last conditional filter before our line must be [all].
    filters = re.findall(r"^\[(\w+)\]", after_uart, re.M)
    assert filters and filters[-1] == "all", filters


def test_existing_enable_uart_is_left_alone(tmp_path: Path) -> None:
    card = _stage_with_config(tmp_path, "arm_64bit=1\nenable_uart=0\n")
    body = (card / "config.txt").read_text()
    assert body.count("enable_uart") == 1, body
    assert "enable_uart=0" in body, "an explicit setting must be respected"


def test_enable_uart_is_idempotent(tmp_path: Path) -> None:
    card = _stage_with_config(tmp_path, "arm_64bit=1\n\n[all]\n")
    _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert (card / "config.txt").read_text().count("enable_uart") == 1


FINALIZE = REPO_ROOT / "deploy" / "pi" / "combiner-finalize.sh"


def test_finalize_waits_for_the_clone_to_have_its_own_identity() -> None:
    """A sealed image has no identity; a clone generates machine-id and host keys
    on ITS first boot. Under a read-only root those writes go to tmpfs and are
    discarded, so the unit would present a different host key every reboot. The
    first boot must therefore run writable."""
    text = FINALIZE.read_text()
    assert "/etc/machine-id" in text
    assert "ssh_host_" in text
    assert "will retry next boot" in text


def test_finalize_refuses_to_lock_a_broken_unit() -> None:
    """Freezing a misconfigured box makes it harder to fix."""
    assert "combiner-apply.service" in FINALIZE.read_text()


def test_finalize_needs_no_network() -> None:
    """It may run in a rack. combiner-seal installs overlayroot at bench time;
    finalize must refuse rather than reach for apt."""
    text = FINALIZE.read_text()
    assert "dpkg -s overlayroot" in text
    # It may MENTION apt-get in advice; it must never invoke it.
    invocations = [ln for ln in text.splitlines() if re.match(r"\s*(\w+=\S+\s+)*apt-get\b", ln)]
    assert not invocations, invocations


def test_finalize_guards_against_a_doubled_token() -> None:
    """raspi-config's disable_overlayfs is a single-occurrence sed, so two
    copies of the token would leave a unit read-only after being 'unlocked'."""
    text = FINALIZE.read_text()
    assert "grep -q 'overlayroot=tmpfs' \"$CMDLINE\"" in text


def test_finalize_keeps_cmdline_single_line() -> None:
    """A mangled cmdline.txt is an unbootable Pi."""
    text = FINALIZE.read_text()
    assert "wc -l" in text
    assert "combiner-prelock" in text


def test_seal_arms_the_overlay_rather_than_enabling_it() -> None:
    text = SEAL.read_text()
    assert "combiner-finalize" in text
    # overlayroot must be installed at bench time so locking needs no network.
    assert "apt-get install -y overlayroot" in text
    assert "--no-overlay" in text


def test_the_two_version_pins_agree() -> None:
    """prep-card.sh stages a release tarball; combiner-firstboot.sh downloads one
    when none is staged. If they disagree, a card can be staged with one version
    and provision itself with another — silently, and only on a unit with no
    tarball on its card."""
    prep = re.search(r'^VERSION_DEFAULT="([^"]+)"', PREP_CARD.read_text(), re.M)
    boot = re.search(r'^COMBINER_VERSION="\$\{COMBINER_VERSION:-([^}]+)\}"', FIRSTBOOT.read_text(), re.M)
    assert prep and boot, (prep, boot)
    assert prep.group(1) == boot.group(1), f"{prep.group(1)} != {boot.group(1)}"


def test_install_ships_finalize_so_seal_can_arm_it() -> None:
    """combiner-seal lives in /usr/local/sbin once installed, where the release
    tree is not. It must not have to find combiner-finalize next to itself."""
    text = INSTALL.read_text()
    assert "/usr/local/sbin/combiner-finalize" in text
    assert "combiner-finalize.service" in text
    # ...and seal must only enable it, never go looking for the source.
    seal = SEAL.read_text()
    assert "SELF_DIR" not in seal
    assert "/usr/local/sbin/combiner-finalize" in seal


def test_install_does_not_delete_the_zram_writeback_file() -> None:
    """Raspberry Pi OS Trixie backs zram with a writeback file at /var/swap via
    /etc/rpi/swap.conf. Deleting it leaves systemd-zram-setup@zram0 permanently
    failed, which destroys `systemctl --failed` as a health signal."""
    text = INSTALL.read_text()
    assert "rm -f /var/swap" not in text
    assert "/etc/rpi/swap.conf" in text
    assert "Mechanism=zram" in text
