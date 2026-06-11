#!/bin/bash
#
# Regression check: re-renders every fixture into a tempdir and diffs
# the fresh summaries against the committed ones in fixtures/summary/.
#
# By default it renders with the Java engine (ondes.file.PlayMidiFile).
# Pass --go to render with the Go port (cmd/p) instead; this is the
# parity check that proves the Go renderer matches the committed Java
# reference summaries. (The same comparison runs as `go test ./regression`.)
#
# Exit 0 if all fixtures pass within tolerance, non-zero otherwise.

set -euo pipefail

ENGINE=java
if [[ "${1:-}" == "--go" ]]; then
    ENGINE=go
    shift
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REG="regression"
FIX="$REG/fixtures"
JAR="build/libs/ondes-all.jar"
LIST="$REG/renders.lst"

TMP=$(mktemp -d -t ondes-check.XXXXXX)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/mid" "$TMP/wav"

if [[ "$ENGINE" == "java" && ! -f "$JAR" ]]; then
    echo "building uberJar..."
    gradle uberJar >/dev/null
fi

GOBIN=""
if [[ "$ENGINE" == "go" ]]; then
    echo "==> building cmd/p"
    GOBIN="$TMP/ondes-p"
    go build -o "$GOBIN" ./cmd/p
fi

if [[ "$ENGINE" == "go" ]]; then
    # The Go path uses the committed MIDI fixtures directly, so it needs
    # no JDK. (The same renders run as `go test ./regression`.)
    echo "==> using committed MIDI fixtures from $FIX/mid"
    cp "$FIX"/mid/*.mid "$TMP/mid/"
else
    echo "==> compiling MakeTestMidi"
    javac -d "$REG" "$REG/MakeTestMidi.java"

    echo "==> generating MIDI inputs in $TMP/mid"
    java -cp "$REG" MakeTestMidi "$TMP/mid"
    # close_encounters.mid is a committed static fixture, not generated.
    cp "$FIX/mid/close_encounters.mid" "$TMP/mid/"
fi

count=$(grep -cvE '^\s*(#|$)' "$LIST")
echo "==> rendering $count fixtures ($ENGINE) into $TMP/wav"
while read -r line; do
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    read -r name midi patch <<<"$line"
    mid="$TMP/mid/$midi.mid"
    wav="$TMP/wav/$name.wav"
    printf '  %-22s\n' "$name"
    if [[ "$ENGINE" == "go" ]]; then
        "$GOBIN" -patch "$patch" "$mid" "$wav" >/dev/null 2>&1
    else
        java -cp "$JAR" ondes.file.PlayMidiFile \
            "$mid" "$wav" "$patch" -overwrite >/dev/null 2>&1
    fi
done < "$LIST"

echo "==> diffing against committed summaries"
python3 "$REG/diff_summaries.py" --ref "$FIX/summary" --wav "$TMP/wav"
