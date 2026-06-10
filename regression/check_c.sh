#!/usr/bin/env bash
# Render every fixture in renders.lst through the C engine (c/build/p) and
# diff each WAV against the committed Java reference summary. This is the
# C-side equivalent of check.sh --go.
#
# Usage: regression/check_c.sh
# Assumes the C port is built (cmake --build c/build). Run from the repo root.
set -euo pipefail

cd "$(dirname "$0")/.."

P=c/build/p
if [[ ! -x "$P" ]]; then
    echo "error: $P not found; build with: cmake -S c -B c/build && cmake --build c/build" >&2
    exit 1
fi

LIST=regression/renders.lst
MID=regression/fixtures/mid
SUM=regression/fixtures/summary
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT

while read -r name midi patch _rest; do
    [[ -z "${name:-}" || "${name:0:1}" == "#" ]] && continue
    "$P" -patch "$patch" "$MID/$midi.mid" "$OUT/$name.wav" >/dev/null
done < "$LIST"

python3 regression/diff_summaries.py --ref "$SUM" --wav "$OUT"
