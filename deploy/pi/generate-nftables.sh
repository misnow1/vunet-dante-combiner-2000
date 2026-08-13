#!/usr/bin/env bash
# Wrapper so docs can call generate-nftables.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
exec python3 "$ROOT/generate-nftables.py" "$@"
