#!/bin/bash
#
# Regression check: re-renders every fixture into a tempdir with the Java
# engine (ondes.file.PlayMidiFile) and diffs the fresh summaries against
# the committed ones in fixtures/summary/.
#
# This is the Java side of the parity suite; the C port is checked with
# check_c.sh (and recycling with check_c_pool.sh), which reuse the same
# committed summaries and diff_summaries.py.
#
# Exit 0 if all fixtures pass within tolerance, non-zero otherwise.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REG="regression"
FIX="$REG/fixtures"
JAR="build/libs/ondes-all.jar"
LIST="$REG/renders.lst"

TMP=$(mktemp -d -t ondes-check.XXXXXX)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/mid" "$TMP/wav"

if [[ ! -f "$JAR" ]]; then
    echo "building uberJar..."
    gradle uberJar >/dev/null
fi

echo "==> compiling MakeTestMidi"
javac -d "$REG" "$REG/MakeTestMidi.java"

echo "==> generating MIDI inputs in $TMP/mid"
java -cp "$REG" MakeTestMidi "$TMP/mid"
# close_encounters.mid is a committed static fixture, not generated.
cp "$FIX/mid/close_encounters.mid" "$TMP/mid/"

count=$(grep -cvE '^\s*(#|$)' "$LIST")
echo "==> rendering $count fixtures (java) into $TMP/wav"
while read -r line; do
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    read -r name midi patch <<<"$line"
    mid="$TMP/mid/$midi.mid"
    wav="$TMP/wav/$name.wav"
    printf '  %-22s\n' "$name"
    java -cp "$JAR" ondes.file.PlayMidiFile \
        "$mid" "$wav" "$patch" -overwrite >/dev/null 2>&1
done < "$LIST"

echo "==> diffing against committed summaries"
python3 "$REG/diff_summaries.py" --ref "$FIX/summary" --wav "$TMP/wav"
