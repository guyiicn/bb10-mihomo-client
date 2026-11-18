#!/bin/sh
# Build configtool + the C++ Cascades app and package the .bar, mono-repo layout.
# Run scripts/build-core.sh first (produces app/build/bin/mihomo + tun-helper).
#
# configtool is a standalone Go module under tools/. The C++ app, its assets and
# bar-descriptor.xml live under app/, which is the working dir for CMake and the
# BlackBerry native packager, so bar-descriptor.xml's paths (build/bin/*, assets,
# build/cmake/*) stay relative to app/ unchanged.
set -eu
REPO=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
RESEARCH=${BB10_PROXY_RESEARCH:-"$HOME/bb10-passport/vpn-research"}
GO="$RESEARCH/toolchain/extracted/go/bin/go"
IMAGE='ghcr.io/xiazy/blackberry10-toolchain@sha256:60ce49510a37ed92368cca010bbaabf97b12d209b9fab179e8491b266bb2578f'
APP="$REPO/app"
TOOLS="$REPO/tools"

mkdir -p "$APP/build/bin"
[ -f "$APP/build/bin/mihomo" ]     || { echo 'Missing mihomo (run scripts/build-core.sh)'     >&2; exit 1; }
[ -f "$APP/build/bin/tun-helper" ] || { echo 'Missing tun-helper (run scripts/build-core.sh)' >&2; exit 1; }

echo "[build] configtool ..."
(cd "$TOOLS/configtool" && GOCACHE="$RESEARCH/toolchain/cache" GOMODCACHE="$RESEARCH/toolchain/modcache" \
 GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=qnx GOARCH=arm GOARM=7 "$GO" build -mod=readonly -o "$APP/build/bin/configtool" .)

dock() { docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges \
 --user "$(id -u):$(id -g)" -v "$APP:/src" -w /src "$IMAGE" "$@"; }

echo "[build] cmake configure ..."
dock cmake -S /src -B /src/build/cmake -DCMAKE_TOOLCHAIN_FILE=/opt/bb10-toolchain.cmake
echo "[build] cmake build ..."
dock cmake --build /src/build/cmake --parallel 2
echo "[build] package .bar ..."
dock blackberry-nativepackager -devMode -package build/bb10-proxy-research.bar bar-descriptor.xml -configuration Device-Debug

echo "[build] done:"
ls -la "$APP/build/bb10-proxy-research.bar"
