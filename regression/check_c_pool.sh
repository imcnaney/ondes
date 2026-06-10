#!/usr/bin/env bash
# Voice-pool parity guard. Renders every fixture twice through the C engine
# - once with voice pooling on (-pool, forcing slot recycling across the
# many sequential notes in each fixture) and once with the proven
# fresh-per-note path - and proves they agree:
#
#   * deterministic fixtures: byte-for-byte identical (a reset bug would
#     leave stale time-domain state in a recycled voice and shift the
#     signal, which shows up as a byte diff);
#   * noise fixtures (pink/ocean2): the RNG makes bit-comparison meaningless,
#     so they are checked against the committed Java summary instead.
#
# Finally it confirms the pooled renders still pass the full 49/49 Java
# parity suite. Run from the repo root after building c/build.
set -euo pipefail
cd "$(dirname "$0")/.."

P=c/build/p
[[ -x "$P" ]] || { echo "build c/build first" >&2; exit 1; }

LIST=regression/renders.lst
MID=regression/fixtures/mid
SUM=regression/fixtures/summary
POOL=$(mktemp -d); FRESH=$(mktemp -d)
trap 'rm -rf "$POOL" "$FRESH"' EXIT

# Fixtures whose patches use the noise/pink oscillator: not bit-reproducible.
is_noise() { case "$1" in pink|pink-ce|ocean2) return 0;; *) return 1;; esac; }

bit_ok=0; bit_fail=0; noise_skip=0
while read -r name midi patch _rest; do
    [[ -z "${name:-}" || "${name:0:1}" == "#" ]] && continue
    "$P" -pool -patch "$patch" "$MID/$midi.mid" "$POOL/$name.wav" >/dev/null
    "$P"       -patch "$patch" "$MID/$midi.mid" "$FRESH/$name.wav" >/dev/null
    if is_noise "$name"; then
        noise_skip=$((noise_skip+1))
        continue
    fi
    if cmp -s "$POOL/$name.wav" "$FRESH/$name.wav"; then
        bit_ok=$((bit_ok+1))
    else
        bit_fail=$((bit_fail+1)); echo "BIT-DIFF  $name (pooled != fresh)"
    fi
done < "$LIST"

echo
echo "pooled vs fresh: $bit_ok bit-identical, $bit_fail differing, $noise_skip noise (summary-checked)"
echo
echo "=== pooled renders vs committed Java summaries ==="
python3 regression/diff_summaries.py --ref "$SUM" --wav "$POOL" | tail -1

[[ $bit_fail -eq 0 ]] || { echo "FAIL: recycled voices diverge from fresh"; exit 1; }
