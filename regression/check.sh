#!/bin/bash
#
# Regression check: re-renders every fixture into a tempdir and diffs
# the fresh summaries against the committed ones in fixtures/summary/.
#
# Exit 0 if all fixtures pass within tolerance, non-zero otherwise.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REG="regression"
FIX="$REG/fixtures"
JAR="build/libs/ondes-all.jar"
LIST="$REG/renders.lst"

if [[ ! -f "$JAR" ]]; then
    echo "building uberJar..."
    gradle uberJar >/dev/null
fi

TMP=$(mktemp -d -t ondes-check.XXXXXX)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/mid" "$TMP/wav"

echo "==> compiling MakeTestMidi"
javac -d "$REG" "$REG/MakeTestMidi.java"

echo "==> generating MIDI inputs in $TMP/mid"
java -cp "$REG" MakeTestMidi "$TMP/mid"
# close_encounters.mid is a committed static fixture, not generated.
cp "$FIX/mid/close_encounters.mid" "$TMP/mid/"

count=$(grep -cvE '^\s*(#|$)' "$LIST")
echo "==> rendering $count fixtures into $TMP/wav"
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
