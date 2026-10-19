#!/bin/sh
# Refresh the embedded Hermes plugin and Pi extension from the Python reference tree (verbatim copies).
# Usage: tools/sync_assets.sh [PATH_TO_REFERENCE]   (default ../reference)
set -eu
cd "$(dirname "$0")/.."
SRC=${1:-../nod}
DST=internal/hosts/assets
rm -rf "$DST/hermes" "$DST/pi"
mkdir -p "$DST/hermes/nod-gate" "$DST/pi"
for f in __init__.py core.py plugin.yaml; do cp "$SRC/hermes/nod-gate/$f" "$DST/hermes/nod-gate/$f"; done
for f in extension.ts policy.mjs; do cp "$SRC/pi/$f" "$DST/pi/$f"; done
echo "synced from $SRC"
