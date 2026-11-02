#!/usr/bin/env bash
# Build the ESP Lab Bridge. The Rust esp32sim engine is built separately and must be
# copied next to the bridge (or pointed at with ESP32SIM_BIN).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$ROOT/dist/bridge"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)}"

mkdir -p "$OUT"
cd "$ROOT"

for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  extension=""
  [ "$goos" = "windows" ] && extension=".exe"
  echo "building $goos/$goarch"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$OUT/esp-lab-bridge-$goos-$goarch$extension" .
done

echo
echo "bridge binaries in $OUT"
echo "copy esp32sim (and esp32sim.exe on Windows) next to each bridge binary."
