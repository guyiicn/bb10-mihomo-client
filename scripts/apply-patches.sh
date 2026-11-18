#!/bin/sh
# Regenerate the mihomo QNX dependency overrides from pinned upstream + patches.
#
# Reads mihomo/UPSTREAM (module import-path, version, local-name), fetches each
# module at its exact version, copies it to mihomo/overrides-upstream/<local-name>
# (writable, gitignored), and applies mihomo/patches/<local-name>.patch (QNX port
# files + build-tag tweaks). The result is a reproducible override tree equivalent
# to the old sibling-directory forks.
#
# Source of upstream module trees, in order of preference:
#   1. $BB10_UPSTREAM_MODCACHE  - an existing GOMODCACHE that already holds the
#      pinned versions (offline; e.g. the research toolchain modcache). No network.
#   2. `go mod download` into a throwaway module (needs network / a GOPROXY).
#
# After this runs, point the build's replaces at mihomo/overrides-upstream/<name>
# instead of the legacy absolute paths (see build-core.sh / go.work). Verify with
#   scripts/verify-overrides.sh  (byte-compares against a reference override tree).
set -eu

REPO=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
UPSTREAM="$REPO/mihomo/UPSTREAM"
PATCHES="$REPO/mihomo/patches"
OUT="$REPO/mihomo/overrides-upstream"
GO="${BB10_GO:-go}"

[ -f "$UPSTREAM" ] || { echo "Missing $UPSTREAM" >&2; exit 1; }
command -v patch >/dev/null 2>&1 || { echo "need 'patch'" >&2; exit 1; }

# Parse UPSTREAM into: path<TAB>version<TAB>name (skip comments/blank).
mods=$(awk '$1=="module"{print $2"\t"$3"\t"$4}' "$UPSTREAM")
[ -n "$mods" ] || { echo "No module lines in $UPSTREAM" >&2; exit 1; }

# Resolve a GOMODCACHE that holds the pinned versions.
modcache=""
if [ -n "${BB10_UPSTREAM_MODCACHE:-}" ] && [ -d "$BB10_UPSTREAM_MODCACHE" ]; then
  modcache="$BB10_UPSTREAM_MODCACHE"
  echo "[apply-patches] using modcache: $modcache"
else
  command -v "$GO" >/dev/null 2>&1 || { echo "need Go (or set BB10_UPSTREAM_MODCACHE)" >&2; exit 1; }
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  ( cd "$tmp"
    "$GO" mod init bb10.local/fetch-overrides >/dev/null 2>&1
    echo "$mods" | while IFS="$(printf '\t')" read -r path ver name; do
      [ -n "$path" ] && "$GO" mod download "$path@$ver"
    done
  )
  modcache=$("$GO" env GOMODCACHE)
  echo "[apply-patches] fetched into GOMODCACHE: $modcache"
fi

rm -rf "$OUT"; mkdir -p "$OUT"
echo "$mods" | while IFS="$(printf '\t')" read -r path ver name; do
  [ -n "$path" ] || continue
  src="$modcache/$path@$ver"
  [ -d "$src" ] || { echo "MISSING upstream: $src (fetch failed?)" >&2; exit 1; }
  dst="$OUT/$name"
  cp -r "$src" "$dst"
  chmod -R u+rw "$dst"
  if [ -f "$PATCHES/$name.patch" ]; then
    ( cd "$dst" && patch -p1 --no-backup-if-mismatch < "$PATCHES/$name.patch" >/dev/null )
    echo "[apply-patches] $name: applied patch"
  else
    echo "[apply-patches] $name: no patch (verbatim upstream)"
  fi
done

echo "[apply-patches] done -> $OUT"
