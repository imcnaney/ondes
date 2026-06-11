# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

OndeSynth is a modular software synthesizer in Java 11. Users build patches by wiring together components (oscillators, filters, envelopes, mixers, etc.) declared in YAML files. The synth listens on a MIDI input device and writes to a JavaSound audio output device, or renders a MIDI file to a WAV.

## Build & run

The project uses Gradle (Java 11, TestNG). The convenience scripts at the repo root assume `.` is on `PATH` and a Bash shell (Cygwin works on Windows). All of them invoke `java -cp build/libs/ondes-all.jar ...`, so the uberJar must be built first.

- `./b` — `gradle uberJar` (builds `build/libs/ondes-all.jar`, the fat jar that every script runs).
- `./br <args>` — build then run `o` with the args.
- `./o <args>` — run the synth. Sources `ondes-args` first (defaults like `-in 828 -out 'main out'`), then appends CLI args. `o -h` for usage; `o -list` lists patches; `o -show-patch <name>` dumps a patch.
- `./p <in.mid> <out.wav>` — renders a MIDI file to WAV via `ondes.file.PlayMidiFile`.
- `./w` — `ondes.tools.WaveEditor` (interactive harmonic wave editor; saves YAML you paste into patches).
- `./midiInfo`, `./midiMon`, `./audioInfo` — list/monitor MIDI and audio devices. Use these to discover the substring you pass to `-in` / `-out` (matched case-insensitively against device labels; first match wins).
- `./prog` — runs `ondes.synth.voice.VoiceMaker` directly.

Tests use TestNG: `gradle test` (or `gradle test --tests ondes.midi.MlzMidiTest` for a single class). `testLogging.showStandardStreams = true` is set, so stdout from tests appears in the Gradle output.

`ondes-args` is sourced by `o`; the synth's own CLI options override it. `-in` / `-out` are required; defaults live there so day-to-day invocations are short.

## Patch files (YAML programs)

Patches are loaded from two locations at startup, in this order:
1. The filesystem `./program/` directory (relative to CWD). Subdirectories are only scanned if `-all` is passed.
2. The classpath resources at `src/main/resources/program/` (built-in patches like `sine`, `saw`, `square`, `bell-organ`).

Filesystem patches win on name collision. The patch name is the filename minus `.yaml`; if names collide use the numeric index from `o -list`. Each component in a patch is a top-level YAML map; the `type` key dispatches via `ComponentMaker.getMonoComponent` (`wave`, `filter`, `env`, `mix`, `dynamic-mix`, `balancer`, `limiter`, `op-amp`, `controller`, `smooth`, `midi-note`, `echo`). Connect components with `out: <other-component>` or `out: <other-component>.<select>` for named inputs; `out: main` sends to the voice's main mix.

Extensive patch-programming docs live in `doc/`, starting at `doc/Voice.md` (linked from the README).

## Architecture

The signal-flow model is **inside-out**: an "output" is really a `WiredIntSupplier` lambda that the *downstream* component pulls from. To avoid infinite loops when a component feeds itself (FM), each `WiredIntSupplier` latches its value on first read per sample, and the main loop resets the "visited" flag every sample via `OndeSynth.resetWires()`. This is the central invariant — anything new that participates in the wire graph must reset per sample.

The main loop is `OndeSynth.run()`: `resetWires()` → `instant.next()` (advances sample counter and all `PhaseClock`s) → `monoMainMix.update()` (pulls one sample through the entire graph and writes to the audio buffer). This thread is hot — no allocations per sample.

Component lifecycle / context:
- A `Voice` is one instantiation of a patch's components. `ChannelVoicePool` pre-creates ~20 voices per MIDI channel to dodge GC pauses; `OndeSynth.VoiceTracker` tracks which (channel, note) is currently sounding.
- Components have a `ComponentContext`: `VOICE` (default, one per voice — paused/resumed with the note), `CHANNEL` (one per MIDI channel — used for echo, LFOs shared across notes, fixed-frequency IIR filters), or `GLOBAL` (main mix and main limiter). Channel-context outputs bypass the per-voice junction and go to the channel mix directly to avoid summing themselves into every voice.
- A voice's components feed a per-voice `DynamicJunction` (`voiceMix`), which connects to the channel mix only while the voice is active — this is how `pause()` / `resume()` cheaply gate idle voices out of the sum.

Signal path: voice components → voiceMix (DynamicJunction) → channelMix → main Limiter (GLOBAL) → MainMix → audio output (or WAV file for `WaveMonoMainMix`). The Limiter is the only non-voice/non-mix output wire that needs manual reset in `resetWires()`.

Entry points (each is a `main`):
- `ondes.App` — live MIDI-driven playback (the `o` script).
- `ondes.file.PlayMidiFile` — MIDI-file-to-WAV rendering (the `p` script).
- `ondes.tools.WaveEditor` — harmonic wave authoring UI (the `w` script).
- `ondes.audio.AudioInfo`, `ondes.midi.MidiInfo`, `ondes.midi.MidiMonitor` — device discovery utilities.
- `ondes.synth.voice.VoiceMaker` (`prog` script) — patch-loading sanity check.

`App` and the wave editor share `SynthSession`, which wires the chosen MIDI input and audio output (`Mixer`) into an `OndeSynth`. JavaSound mixer naming is counterintuitive — audio output requires a mixer with a *source* line (see `doc/JavaSoundNaming.md`).

## Things to know

- `o -log-main-out` dumps verbose timing data to `update.log` (see `doc/timing.md`). The default audio buffer is 2048 samples; raise with `-buffer-size` if you hear gaps, lower for snappier MIDI response.
- The limiter prints `<>` diamonds on overflow — that means a component is producing too much amplitude, not a code bug.
- The synth tolerates dropped frames but is not suitable for tight-timing live performance — JavaSound on Windows still uses the old MME API rather than WASAPI. See `doc/AudioBuffer.md`.
- `-hold` (or `App.hold`) suppresses note-offs to sustain drones.

## Working agreements

- Always follow best practices.
- Always write documentation for changes.
- Always write tests for changes.
