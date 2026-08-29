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
GO_LIVE = REPO_ROOT / "deploy" / "pi" / "combiner-go-live.sh"
LED = REPO_ROOT / "deploy" / "pi" / "combiner-led.sh"
SYSTEMD = REPO_ROOT / "deploy" / "pi" / "systemd"

# The provisioning hold. Six files have to agree on this name, and nothing at
# runtime would notice if one of them drifted: a unit whose marker is spelled
# differently simply applies show addressing on a bench LAN and vanishes.
HOLD_MARKER_NAME = "combiner-provisioning"

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


@pytest.mark.parametrize("script", [FIRSTBOOT, PREP_CARD, APPLY, INSTALL, SEAL, GO_LIVE, LED])
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
    # overlayroot arrives during provisioning, not here: sealing may run on a
    # unit with no network. Seal only checks for it.
    assert "dpkg -s overlayroot" in text
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


def test_install_retires_the_orphaned_zram_writeback_timer() -> None:
    """rpi-zram-writeback.timer is emitted only for the zram+file mechanism.
    Switching to zram stops it being emitted, and a running copy becomes
    not-found and lands in failed state — leaving a freshly provisioned unit
    with a failed unit, which is the health-signal problem this avoids."""
    text = INSTALL.read_text()
    assert "stop rpi-zram-writeback.timer" in text
    assert "reset-failed rpi-zram-writeback.timer" in text


LOCK = REPO_ROOT / "deploy" / "pi" / "combiner-lock.sh"


def test_install_ships_combiner_lock() -> None:
    assert "/usr/local/sbin/combiner-lock" in INSTALL.read_text()


def test_lock_preflights_at_the_bench_not_at_boot() -> None:
    """The point of this script is that a person is watching when it fails."""
    text = LOCK.read_text()
    assert "/etc/machine-id" in text
    assert "ssh_host_" in text
    assert "combiner -check" in text


def test_lock_refuses_a_sealed_card() -> None:
    """A sealed card has no identity; locking it would freeze a unit with no
    host keys and no machine-id of its own."""
    assert "looks like a sealed card" in LOCK.read_text()


def test_lock_off_keeps_cmdline_single_line() -> None:
    text = LOCK.read_text()
    assert "wc -l" in text
    assert "multi-line cmdline.txt" in text


def test_seal_refuses_a_locked_root() -> None:
    """Every step seal takes writes to /etc or /var. Under a read-only overlay
    those land in tmpfs, so seal would report success and then be undone by the
    next reboot — and the card cloned from it would carry an identity that was
    never cleared, giving the whole fleet one machine-id and one set of host
    keys. Silent, and the exact failure seal exists to prevent."""
    text = SEAL.read_text()
    assert "overlayroot=tmpfs' /proc/cmdline" in text
    assert "combiner-lock --off" in text
    # The guard must come before anything destructive.
    guard = text.index("overlayroot=tmpfs' /proc/cmdline")
    first_write = text.index("truncate -s 0 /etc/machine-id")
    assert guard < first_write, "the guard must precede the first write"


def test_overlayroot_is_a_provisioning_dependency(user_data: dict[str, Any]) -> None:
    """Every unit locks its root eventually, and that step may run in a rack with
    no network. Installing overlayroot at lock time made locking need a mirror;
    it belongs in provisioning, where the bench network is."""
    assert "overlayroot" in set(user_data["packages"])


@pytest.mark.parametrize("script", [LOCK, SEAL], ids=["combiner-lock", "combiner-seal"])
def test_lock_and_seal_never_invoke_apt(script: Path) -> None:
    """They may run on a unit already racked."""
    invocations = [ln for ln in script.read_text().splitlines() if re.match(r"\s*(\w+=\S+\s+)*apt-get\b", ln)]
    assert not invocations, invocations


# --------------------------------------------------------------------------
# Two-phase provisioning: first boot holds on DHCP, combiner-go-live commits.
# --------------------------------------------------------------------------

HOLD_USERS = [
    (PREP_CARD, "prep-card"),
    (FIRSTBOOT, "combiner-firstboot"),
    (APPLY, "combiner-apply"),
    (GO_LIVE, "combiner-go-live"),
    (LED, "combiner-led"),
    (SEAL, "combiner-seal"),
    (FINALIZE, "combiner-finalize"),
]


@pytest.mark.parametrize("script,name", HOLD_USERS, ids=[n for _, n in HOLD_USERS])
def test_the_hold_marker_name_agrees_everywhere(script: Path, name: str) -> None:
    """A drifted name fails silently and expensively: the unit skips the hold,
    applies show addressing on a bench LAN, and is gone."""
    assert HOLD_MARKER_NAME in script.read_text()


def test_prep_card_writes_the_provisioning_hold(tmp_path: Path) -> None:
    """Written at staging time, not by the unit, so a card holds from the moment
    it is prepared — including one whose provisioning fails outright."""
    r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
    assert r.returncode == 0, r.stderr
    assert (tmp_path / "card" / HOLD_MARKER_NAME).exists()


def test_restaging_a_live_card_puts_it_back_into_the_hold(tmp_path: Path) -> None:
    """Re-staging means 'provision this again', and that has to include the hold
    — otherwise a card pulled from a running unit and re-staged would go straight
    back to show addressing without anyone verifying it."""
    for _ in range(2):
        r = _prep_card(tmp_path, "--password-hash", GOOD_HASH)
        assert r.returncode == 0, r.stderr
    assert (tmp_path / "card" / HOLD_MARKER_NAME).exists()


def test_firstboot_defers_activation() -> None:
    """The whole point: provisioning must not apply the config or touch the
    network managers, so the unit stays reachable on the bench it booted on."""
    text = FIRSTBOOT.read_text()
    assert "--defer-activation" in text
    assert re.search(r"install\.sh\S*\s+--defer-activation", text), text


def test_firstboot_creates_the_hold_for_a_hand_staged_card() -> None:
    """The Windows path in docs/sd-image.md copies files by hand, with no
    prep-card run to write the marker."""
    text = FIRSTBOOT.read_text()
    assert ': >"$HOLD_MARKER"' in text
    marker_at = text.index("created $HOLD_MARKER")
    install_at = text.index("--defer-activation /etc/combiner/site.yaml")
    assert marker_at < install_at, "the hold must exist before install.sh runs"


def test_install_defers_the_network_manager_swap() -> None:
    """Under --defer-activation the three steps that take the box off the bench
    network — the networkd swap, enabling the units, and the forced apply — must
    all sit behind the guard."""
    text = INSTALL.read_text()
    guard = text.index('if [[ "$DEFER_ACTIVATION" -eq 0 ]]; then')
    for step in (
        "systemctl disable --now NetworkManager",
        "systemctl enable systemd-networkd",
    ):
        assert text.index(step) > guard, step
    apply_guard = text.rindex('if [[ "$DEFER_ACTIVATION" -eq 0 ]]; then')
    for step in (
        "systemctl enable nftables combiner combiner-apply",
        "--force",
    ):
        assert text.index(step) > apply_guard, step


def test_install_ships_go_live_and_the_led_helper() -> None:
    """combiner-go-live is what releases a deferred install; a unit missing it
    would be held with no way out short of editing the card."""
    text = INSTALL.read_text()
    assert "/usr/local/sbin/combiner-go-live" in text
    assert "/usr/local/sbin/combiner-led" in text
    assert "combiner-signal.service" in text


def test_install_stages_allowlists_when_deferring() -> None:
    """The release tree may be gone by the time go-live runs, and combiner-apply
    refuses to apply without allowlists — it would die rather than apply."""
    text = INSTALL.read_text()
    # Between the forced apply (the non-deferred branch) and the end of the
    # guard is the deferred branch, and that is where the staging has to be.
    branch = text.index("systemctl enable nftables combiner combiner-apply")
    assert text.index('cp -a "$ROOT/config/allowlists/." /etc/combiner/allowlists/') > branch


def test_apply_holds_while_the_marker_is_present() -> None:
    """This is what makes the second boot automatic: go-live only removes the
    marker and reboots, and the existing every-boot unit does the rest."""
    text = APPLY.read_text()
    hold = text.index('if [[ -e "$HOLD_MARKER"')
    first_write = text.index("write_forwarding 0")
    assert hold < first_write, "the hold must precede anything that changes the box"


def test_apply_exempts_dry_run_from_the_hold() -> None:
    """combiner-go-live validates with --dry-run while the hold is by definition
    still in place. If the hold swallowed that, go-live would commit to a config
    nothing had checked."""
    text = APPLY.read_text()
    guard = re.search(r'if \[\[ -e "\$HOLD_MARKER".*?\]\]; then', text, re.S)
    assert guard, text
    assert "$DRY_RUN" in guard.group(0)
    assert "$FORCE" in guard.group(0)


def _go_live_forward_path() -> str:
    """Just the go-live direction. --undo comes earlier in the file and has its
    own reboot, so a whole-file index() would match the wrong one."""
    text = GO_LIVE.read_text()
    return text[text.index("# --- guards ---") :]


def test_go_live_validates_before_it_commits() -> None:
    """The unit is reachable right up until the reboot, and unreachable after.
    A config that would be rejected has to be found on the near side of that."""
    text = _go_live_forward_path()
    dry_run = text.index("--dry-run")
    for later in ("systemctl enable systemd-networkd", 'rm -f "$HOLD_MARKER"', "systemctl reboot"):
        assert text.index(later) > dry_run, later


def test_go_live_does_not_tear_down_the_network_manager_it_is_running_over() -> None:
    """--now here would kill the SSH session before the marker is cleared,
    leaving a unit both unreachable and still held. The reboot does the swap."""
    text = GO_LIVE.read_text()
    assert not re.search(r"systemctl disable --now\s+(NetworkManager|dhcpcd|\"\$svc\")", text), text
    assert 'systemctl disable "$svc"' in text


def test_go_live_clears_the_hold_last() -> None:
    """A half-released unit — marker gone, units not enabled — comes up on show
    addressing with nothing running."""
    text = _go_live_forward_path()
    assert text.index("systemctl enable nftables combiner combiner-apply") < text.index('rm -f "$HOLD_MARKER"')


def test_go_live_undo_unpicks_the_handover_not_just_the_marker() -> None:
    """Re-creating the marker on a live unit is not enough: the hold stops
    combiner-apply from applying a config, it does not unpick one already
    applied. Without the handover back, the unit returns on show addressing —
    held, and just as unreachable as before."""
    text = GO_LIVE.read_text()
    undo = text[text.index('if [[ "$ACTION" == "undo" ]]; then') :]
    assert "systemctl enable NetworkManager" in undo
    assert "systemctl disable systemd-networkd" in undo
    assert "combiner*.netdev" in undo
    assert "99-combiner.conf" in undo
    # The marker first, so a failure part-way leaves a unit that holds rather
    # than one that re-applies the config it is being taken off.
    assert undo.index(': >"$HOLD_MARKER"') < undo.index("systemctl disable systemd-networkd")


def test_go_live_refuses_a_locked_root() -> None:
    """Every step is a systemctl enable/disable, which writes under /etc. Under
    an overlay those evaporate on the very reboot this script performs."""
    text = GO_LIVE.read_text()
    assert "overlayroot=tmpfs' /proc/cmdline" in text
    assert "combiner-lock --off" in text
    guard = text.index("overlayroot=tmpfs' /proc/cmdline")
    first_write = text.index("systemctl enable systemd-networkd")
    assert guard < first_write, "the guard must precede the first write"


@pytest.mark.parametrize("script", [GO_LIVE, LED], ids=["combiner-go-live", "combiner-led"])
def test_go_live_and_led_need_no_network(script: Path) -> None:
    """Both run on a unit that may already be racked."""
    text = script.read_text()
    invocations = [ln for ln in text.splitlines() if re.match(r"\s*(\w+=\S+\s+)*(apt-get|curl|wget)\b", ln)]
    assert not invocations, invocations


@pytest.mark.parametrize("state", ["provisioning", "ready", "failed", "running", "auto", "off"])
def test_led_succeeds_without_an_led(state: str) -> None:
    """There is no LED on a laptop or an amd64 host, and a unit whose LED cannot
    be driven must still provision, go live and run — every caller is on a path
    that must not fail. Run for real: this is the whole contract."""
    r = subprocess.run(["bash", str(LED), state], capture_output=True, text=True)
    assert r.returncode == 0, r.stderr


def test_led_rejects_a_misspelled_state() -> None:
    """Validated before the hardware probe, on purpose. A typo is a caller bug
    on every machine, and most machines this is edited on have no LED — exiting
    0 there is how a broken call would reach a Pi."""
    r = subprocess.run(["bash", str(LED), "bogus"], capture_output=True, text=True)
    assert r.returncode == 1
    assert "unknown state" in r.stderr


def test_led_signal_unit_runs_last() -> None:
    """It reports what happened, not what was about to."""
    unit = (SYSTEMD / "combiner-signal.service").read_text()
    assert "After=combiner-apply.service combiner.service" in unit
    assert "combiner-led auto" in unit
    assert "WantedBy=multi-user.target" in unit


def test_seal_accepts_a_held_golden_unit() -> None:
    """Sealing a held unit is the normal case, not an error. Seal strips
    /etc/combiner/site.yaml anyway — each clone brings its own — so the golden
    unit never needed to apply one, and insisting it go live first would mean
    putting a production config onto a bench that cannot satisfy those VLANs,
    leaving the very unit about to be imaged unreachable and failing."""
    text = SEAL.read_text()
    assert "WAS_HELD" in text
    assert 'die "this unit is still holding' not in text


def test_seal_takes_a_held_unit_live_before_imaging_it() -> None:
    """A held unit was installed with --defer-activation, so nftables, combiner
    and combiner-apply are installed and NOT enabled. Imaging that produces a
    fleet that boots, applies nothing, and looks fine. Delegated to
    combiner-go-live rather than duplicated, so "live" has one definition."""
    text = SEAL.read_text()
    assert "/usr/local/sbin/combiner-go-live --yes --no-reboot" in text
    # go-live validates against /etc/combiner/site.yaml, which seal removes.
    assert text.index("combiner-go-live --yes --no-reboot") < text.index("rm -f /etc/combiner/site.yaml")


def test_seal_images_no_provisioning_hold() -> None:
    """A card imaged with the marker on it strands a whole fleet waiting for a
    combiner-go-live nobody can run in a rack."""
    text = SEAL.read_text()
    assert 'rm -f "$HOLD_MARKER"' in text
    # Before the closing instructions, or the operator images a card that holds.
    assert text.index('rm -f "$HOLD_MARKER"') < text.index("Image the card")


def test_finalize_refuses_while_held() -> None:
    """A held unit's combiner-apply.service succeeds without applying anything,
    so the 'is it configured' check would pass on a box that never was."""
    text = FINALIZE.read_text()
    hold = text.index('if [[ -e "$HOLD_MARKER" ]]; then')
    lock = text.index("locking root filesystem read-only")
    assert hold < lock


def test_finalize_condition_covers_both_boot_paths() -> None:
    """The script falls back to /boot on images that predate /boot/firmware; a
    condition naming only the new path would skip the unit on exactly those."""
    unit = (SYSTEMD / "combiner-finalize.service").read_text()
    assert "ConditionPathExists=|/boot/firmware/cmdline.txt" in unit
    assert "ConditionPathExists=|/boot/cmdline.txt" in unit


MAKEFILE = REPO_ROOT / "Makefile"


def _make_recipe(target: str) -> str:
    """The body of one Makefile target. Searching the whole file instead is how
    the first version of this test passed while the package was broken: the
    shellcheck target names the same scripts."""
    make = MAKEFILE.read_text()
    body = re.search(rf"^{target}:[^\n]*\n((?:[\t#].*\n|\n)*)", make, re.M)
    assert body, f"no {target} target in the Makefile"
    return body.group(1)


def test_the_release_package_ships_everything_install_needs() -> None:
    """install.sh installs from the unpacked release tree, so a file it names
    that `make package` does not copy is a release that fails on first boot —
    and only there. The package's cp list is explicit and drifts silently: it
    already missed combiner-go-live.sh and combiner-led.sh once."""
    package = _make_recipe("package")
    # Directories the package copies wholesale, so their contents need no entry.
    wholesale = ("systemd/", "cloud-init/")
    for d in wholesale:
        assert f"deploy/pi/{d.rstrip('/')}" in package, d
    needed = sorted(re.findall(r'"\$ROOT/deploy/pi/([^"]+)"', INSTALL.read_text()))
    missing = [f for f in needed if not f.startswith(wholesale) and f"deploy/pi/{f} " not in package]
    assert not missing, f"make package does not ship: {missing}"


def test_the_release_package_makes_its_scripts_executable() -> None:
    """install.sh runs them out of the unpacked tree, and tar preserves whatever
    mode the staging copy had."""
    package = _make_recipe("package")
    chmod = package[package.index("chmod 755") :]
    for script in ("combiner-go-live.sh", "combiner-led.sh", "combiner-lock.sh"):
        assert f"deploy/pi/{script}" in chmod, script


def test_firstboot_logs_where_to_find_the_unit() -> None:
    """A held unit provisions on DHCP: its address is not one anybody chose, and
    install.sh masks avahi even when deferring, so combiner.local will not answer
    either. Everything firstboot prints is teed to the FAT partition, so the
    address is readable by pulling the card when the console was not connected.
    Both paths need it — a FAILED provision is when finding the unit matters
    most, and it is still on the network it booted with."""
    text = FIRSTBOOT.read_text()
    assert "ip -4 -br addr show scope global" in text
    body = text[text.index("addresses() {") :]
    # Piping straight into sed masks a failing or empty ip(8) behind sed's exit
    # status, printing a bare "Reachable at:" and nothing else.
    assert "|| true" in body[: body.index("}")], "ip(8) failure must not be swallowed by a pipeline"
    fail_body = text[text.index("fail() {") : text.index('say "started')]
    assert "\n  addresses\n" in fail_body, "the FAILED banner must say where the unit is"
    closing = text[text.index('say "provisioning complete"') :]
    assert "\naddresses\n" in closing, "the success banner must say where the unit is"


def test_go_live_status_validates_in_the_layout_the_unit_uses() -> None:
    """allowlist_files resolve relative to the config's OWN directory, and the
    card has no allowlists/ next to combiner-site.yaml — only /etc does. Checking
    the card copy in place fails on a perfectly good config: the lab Pi reported
    "config valid: NO" about one combiner-apply then applied without complaint.
    prep-card.sh:validate_site_config and combiner-apply both stage a copy first;
    this has to as well."""
    text = GO_LIVE.read_text()
    assert "stage_config" in text
    assert "/etc/combiner/allowlists" in text
    # No check may run against the card path directly.
    for bad in ('-check -config "$BOOT_CONFIG"', '-config "$BOOT_CONFIG" -print-facts'):
        assert bad not in text, f"validates the card copy in place: {bad}"


def test_prep_card_and_go_live_agree_on_how_to_validate() -> None:
    """Both answer "would this config be accepted?" and must answer it the same
    way, or the bench and the unit disagree about the same file."""
    for script in (PREP_CARD, GO_LIVE):
        text = script.read_text()
        assert "allowlists" in text, script.name
        assert "site.yaml" in text, script.name


def test_a_held_unit_keeps_mdns() -> None:
    """The hold's whole promise is that the unit stays reachable, and it comes up
    on a DHCP address nobody chose — so combiner.local is how you find it. Avahi
    only conflicts with the combiner mDNS reflector on udp/5353 once that
    reflector exists, which on a held unit it does not. Masking it during
    provisioning took away the one thing that made a held unit findable."""
    install = INSTALL.read_text()
    mask_at = install.index("systemctl mask avahi-daemon")
    guard_at = install.index('if [[ "$DEFER_ACTIVATION" -eq 0 ]]; then')
    assert guard_at < mask_at, "avahi must not be masked when activation is deferred"
    # And go-live has to pick it up, or a live unit would run both.
    go_live = GO_LIVE.read_text()
    assert "systemctl mask avahi-daemon" in go_live
    assert go_live.index("mask avahi-daemon") < go_live.index('rm -f "$HOLD_MARKER"')


def test_undo_restores_mdns() -> None:
    """Symmetry with the handover: --undo puts the unit back on a bench, where
    being findable is the point."""
    text = GO_LIVE.read_text()
    undo = text[text.index('if [[ "$ACTION" == "undo" ]]; then') : text.index("# --- guards ---")]
    assert "unmask avahi-daemon" in undo


def test_led_signal_fires_only_once_it_can() -> None:
    """combiner-led ships in the release tarball, so a call before the tree is
    unpacked is a silent no-op — which is what the first version did while the
    docs promised a heartbeat."""
    text = FIRSTBOOT.read_text()
    unpack_at = text.index('mv "$TREE" "$INSTALL_ROOT"')
    first_call = text.index("\nled provisioning")
    assert first_call > unpack_at, "led provisioning must follow the unpack that provides the helper"


@pytest.mark.parametrize("branch", ["live", "undo"])
def test_both_reboots_pause_and_say_what_ctrl_c_does(branch: str) -> None:
    """By the time either path reboots it is already committed — the marker and
    the unit states are written. Calling the pause a cancel would be a lie at
    the one moment the operator most needs to trust this output."""
    text = GO_LIVE.read_text()
    undo = text[text.index('if [[ "$ACTION" == "undo" ]]; then') : text.index("# --- guards ---")]
    section = undo if branch == "undo" else text[text.index("# --- guards ---") :]
    assert "REBOOT_DELAY" in section, branch
    assert "Ctrl-C stops the reboot, not the" in section, branch
    # The tty test must be captured before the tee redirect makes it useless.
    assert "INTERACTIVE" in section, branch
