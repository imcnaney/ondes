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

if [[ ! -f "$JAR" ]]; then
    echo "building uberJar..."
    gradle uberJar >/dev/null
fi

# Each line:  <wav-name>  <midi-name>  <patch-name>
# wav-name omits the .wav suffix.
RENDERS=(
    "sine               scale  sine"
    "saw                scale  saw"
    "square             scale  square"
    "ramp-up            scale  ramp-up"
    "ramp-down          scale  ramp-down"
    "bassoon            scale  bassoon"
    "buzz               scale  buzz"
    "bell-organ         scale  bell-organ"
    "octave-organ       scale  octave-organ"
    "no-fundamental     scale  no-fundamental"
    "pwm                scale  pwm"
    "square-filtered    scale  square-filtered"
    "ah-fm              scale  ah-fm"
    "bell               scale  program/bell"
    "brass              scale  brass"
    "glock              scale  glock"
    "ring-mod           scale  ring-mod"
    "fade-octave        scale  fade-octave"
    "fourths            scale  fourths"
    "repeater           scale  repeater"
    "pink               scale  pink"
    "sine-chord         chord  sine"
    "bell-organ-chord   chord  bell-organ"

    "sine-ce            close_encounters  sine"
    "saw-ce             close_encounters  saw"
    "square-ce          close_encounters  square"
    "ramp-up-ce         close_encounters  ramp-up"
    "ramp-down-ce       close_encounters  ramp-down"
    "bassoon-ce         close_encounters  bassoon"
    "buzz-ce            close_encounters  buzz"
    "bell-organ-ce      close_encounters  bell-organ"
    "octave-organ-ce    close_encounters  octave-organ"
    "no-fundamental-ce  close_encounters  no-fundamental"
    "pwm-ce             close_encounters  pwm"
    "square-filtered-ce close_encounters  square-filtered"
    "ah-fm-ce           close_encounters  ah-fm"
    "bell-ce            close_encounters  program/bell"
    "brass-ce           close_encounters  brass"
    "glock-ce           close_encounters  glock"
    "ring-mod-ce        close_encounters  ring-mod"
    "fade-octave-ce     close_encounters  fade-octave"
    "fourths-ce         close_encounters  fourths"
    "repeater-ce        close_encounters  repeater"
    "pink-ce            close_encounters  pink"
)

echo "==> compiling MakeTestMidi"
javac -d "$REG" "$REG/MakeTestMidi.java"

echo "==> generating MIDI fixtures"
java -cp "$REG" MakeTestMidi "$FIX/mid"

echo "==> rendering ${#RENDERS[@]} WAVs"
for line in "${RENDERS[@]}"; do
    read -r name midi patch <<<"$line"
    mid="$FIX/mid/$midi.mid"
    wav="$FIX/wav/$name.wav"
    log="$FIX/log/$name.log"
    json="$FIX/summary/$name.json"
    printf '  %-22s  midi=%-6s  patch=%s\n' "$name" "$midi" "$patch"
    java -cp "$JAR" ondes.file.PlayMidiFile \
        "$mid" "$wav" "$patch" -overwrite >"$log" 2>&1
    python3 "$REG/summarize.py" "$wav" "$json" >/dev/null
done

echo "==> done. fixtures in $FIX/"
