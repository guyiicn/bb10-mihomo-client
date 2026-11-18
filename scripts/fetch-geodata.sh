#!/bin/sh
# Download the GeoIP / GeoSite databases that split-routing needs into assets/.
# They are large public data files (MetaCubeX meta-rules-dat) and are not checked
# into the repo; run this once before ./build.sh if you want split routing.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
BASE='https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest'
mkdir -p "$ROOT/assets"
echo "Fetching geoip.metadb ..."
curl -fsSL -o "$ROOT/assets/geoip.metadb" "$BASE/geoip.metadb"
echo "Fetching geosite.dat ..."
curl -fsSL -o "$ROOT/assets/geosite.dat" "$BASE/geosite.dat"
echo "Done. assets/geoip.metadb + assets/geosite.dat are in place."
