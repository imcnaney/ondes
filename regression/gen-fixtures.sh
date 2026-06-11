#!/bin/bash
#
# Generates reference MIDI + WAV + summary fixtures for the Java engine.
# Rerun this on every change to a component that should affect output.
#
# Output:
#   regression/fixtures/mid/*.mid       - deterministic MIDI inputs
#   regression/fixtures/wav/*.wav       - per-(patch,mid) reference renders
#   regression/fixtures/summary/*.json  - statistical summaries for diffing
#   regression/fixtures/log/*.log       - PlayMidiFile stdout/stderr

set -euo pipefail

# Run from the repo root.
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

echo "==> compiling MakeTestMidi"
javac -d "$REG" "$REG/MakeTestMidi.java"

echo "==> generating MIDI fixtures"
java -cp "$REG" MakeTestMidi "$FIX/mid"

count=$(grep -cvE '^\s*(#|$)' "$LIST")
echo "==> rendering $count WAVs (from $LIST)"
while read -r line; do
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    read -r name midi patch <<<"$line"
    mid="$FIX/mid/$midi.mid"
    wav="$FIX/wav/$name.wav"
    log="$FIX/log/$name.log"
    json="$FIX/summary/$name.json"
    printf '  %-22s  midi=%-18s  patch=%s\n' "$name" "$midi" "$patch"
    java -cp "$JAR" ondes.file.PlayMidiFile \
        "$mid" "$wav" "$patch" -overwrite >"$log" 2>&1
    python3 "$REG/summarize.py" "$wav" "$json" >/dev/null
done < "$LIST"

echo "==> done. fixtures in $FIX/"
