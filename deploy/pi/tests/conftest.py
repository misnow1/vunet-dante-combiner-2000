"""Shared fixtures for deploy/pi generator tests."""

from __future__ import annotations

import importlib.util
import sys
import types
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]
DEPLOY_PI = Path(__file__).resolve().parents[1]
CONFIG_DIR = REPO_ROOT / "config"


def load_script(name: str, filename: str) -> types.ModuleType:
    path = DEPLOY_PI / filename
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


@pytest.fixture(scope="session")
def nft_mod() -> types.ModuleType:
    return load_script("combiner_generate_nftables", "generate-nftables.py")


@pytest.fixture(scope="session")
def net_mod() -> types.ModuleType:
    return load_script("combiner_generate_network_config", "generate-network-config.py")


@pytest.fixture(scope="session")
def site_config_mod() -> types.ModuleType:
    return load_script("combiner_site_config", "site_config.py")


@pytest.fixture
def site_example() -> Path:
    """Default profile: audio trunk — Dante untagged (PVID), Control tagged."""
    return CONFIG_DIR / "site.example.yaml"


@pytest.fixture
def site_tagged_trunk() -> Path:
    return CONFIG_DIR / "site.tagged-trunk.example.yaml"


@pytest.fixture
def site_lab_flat() -> Path:
    return CONFIG_DIR / "site.lab-flat.example.yaml"
