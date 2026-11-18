#!/bin/sh
# Byte-verify that the regenerated overrides (mihomo/overrides-upstream, produced
# by apply-patches.sh) are identical to a reference override tree. This proves the
# pin + patch set is complete and correct: a fresh checkout rebuilds the exact
# dependency source the working binary was built from.
#
# Reference tree: $BB10_OVERRIDES_REF, else the legacy research forks if present.
# Exits non-zero (and lists differences) on any mismatch.
set -eu

REPO=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
OUT="$REPO/mihomo/overrides-upstream"
REF="${BB10_OVERRIDES_REF:-$HOME/bb10-passport/vpn-research/overrides}"

[ -d "$OUT" ] || { echo "No $OUT — run scripts/apply-patches.sh first" >&2; exit 1; }
[ -d "$REF" ] || { echo "No reference override tree at $REF (set BB10_OVERRIDES_REF)" >&2; exit 1; }

rc=0
for d in "$OUT"/*/; do
  name=$(basename "$d")
  if [ ! -d "$REF/$name" ]; then echo "EXTRA: $name not in reference" >&2; rc=1; continue; fi
  if diff -rq "$REF/$name" "$d" >/tmp/.ov_diff 2>&1; then
    echo "OK   $name  (byte-identical)"
  else
    echo "DIFF $name:" >&2; sed "s#$OUT#<regen>#g;s#$REF#<ref>#g" /tmp/.ov_diff >&2; rc=1
  fi
done
for d in "$REF"/*/; do
  name=$(basename "$d")
  [ -d "$OUT/$name" ] || { echo "MISSING: reference has $name, regen does not" >&2; rc=1; }
done
rm -f /tmp/.ov_diff
[ "$rc" -eq 0 ] && echo "[verify] all overrides byte-identical to reference" || echo "[verify] MISMATCH (see above)" >&2
exit "$rc"
