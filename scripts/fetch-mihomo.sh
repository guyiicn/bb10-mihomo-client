#!/bin/sh
# Fetch the pinned mihomo core source and apply the QNX patch, reproducibly.
#
# Reads the `mihomo` pin from mihomo/UPSTREAM (upstream repo + exact commit),
# blobless-clones that commit into mihomo/upstream/ (gitignored), strips the VCS
# metadata, and applies mihomo/patches/mihomo.patch (the QNX DNS packetconn port +
# go.mod/go.sum deltas). The result is the exact mihomo source the QNX binary is
# built from — no dependency on any external checkout.
#
# The patch intentionally does NOT carry the 7 local-path dependency `replace`
# directives; those are injected at build time by build-core.sh into go.work,
# pointing at mihomo/overrides-upstream/ (see scripts/apply-patches.sh). This keeps
# mihomo/patches/mihomo.patch free of absolute paths and fully portable.
#
# Env: BB10_MIHOMO_GIT overrides the clone URL (e.g. a local mirror for offline).
#      Pass --force to re-fetch over an existing mihomo/upstream.
set -eu

REPO=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
UPSTREAM="$REPO/mihomo/UPSTREAM"
PATCH="$REPO/mihomo/patches/mihomo.patch"
OUT="$REPO/mihomo/upstream"

[ -f "$UPSTREAM" ] || { echo "Missing $UPSTREAM" >&2; exit 1; }
[ -f "$PATCH" ] || { echo "Missing $PATCH" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "need git" >&2; exit 1; }
command -v patch >/dev/null 2>&1 || { echo "need patch" >&2; exit 1; }

force=""
[ "${1:-}" = "--force" ] && force=1
if [ -d "$OUT" ] && [ -z "$force" ]; then
  echo "[fetch-mihomo] $OUT exists (use --force to re-fetch); skipping"
  exit 0
fi

# Parse the `mihomo` pin line: keyword  import-path  commit  localdir
set -- $(awk '$1=="mihomo"{print $2, $3; exit}' "$UPSTREAM")
path="${1:-}"; commit="${2:-}"
[ -n "$path" ] && [ -n "$commit" ] || { echo "No 'mihomo' pin in $UPSTREAM" >&2; exit 1; }
url="${BB10_MIHOMO_GIT:-https://$path}"

echo "[fetch-mihomo] clone $url @ $commit"
rm -rf "$OUT"
git clone --filter=blob:none --no-checkout "$url" "$OUT" >/dev/null 2>&1
git -C "$OUT" checkout -q "$commit"
rm -rf "$OUT/.git"
echo "[fetch-mihomo] apply $PATCH"
( cd "$OUT" && patch -p1 --no-backup-if-mismatch < "$PATCH" >/dev/null )
echo "[fetch-mihomo] done -> $OUT (mihomo @ ${commit}%.12s + QNX patch)"
