# The Go port

OndeSynth has a Go port of the synth engine and tooling alongside the
original Java. The Go module is rooted at the repo top level (`module
ondes` in `go.mod`); the Java tree under `src/main/java` is unchanged and
remains the reference implementation.

## What the Go port covers

| Capability | Java | Go | Notes |
|---|---|---|---|
| MIDI file → WAV render | `ondes.file.PlayMidiFile` (`./p`) | `cmd/p` | At parity — see below |
| Live MIDI → audio | `ondes.App` (`./o`) | `cmd/o` | malgo (CoreAudio) + gomidi/rtmidi |
| Audio device list | `ondes.audio.AudioInfo` | `cmd/audioInfo` | |
| MIDI device list | `ondes.midi.MidiInfo` | `cmd/midiInfo` | |
| MIDI monitor | `ondes.midi.MidiMonitor` | `cmd/midiMon` | |
| Wave editor GUI | `ondes.tools.WaveEditor` (`./w`) | — | Not ported |

All 11 patch component types (wave, filter, env, mix/dynamic-mix,
balancer, limiter, op-amp, controller, smooth, midi-note, echo) are
implemented under `component/`.

## Commands

```
# render a MIDI file through a patch to WAV
go run ./cmd/p -patch <name> in.mid out.wav

# play a patch live from a MIDI keyboard
go run ./cmd/o -in <port-substr> -out <device-substr> -patch <name>
go run ./cmd/o -list                       # list MIDI inputs + audio outputs
go run ./cmd/o -hold ...                    # suppress note-offs (drone)
go run ./cmd/o -buffer-size 256 ...         # smaller = lower latency
go run ./cmd/o -patch brass -patch 2:sine   # multi-timbral: default + ch-2 override

# device utilities
go run ./cmd/audioInfo
go run ./cmd/midiInfo
go run ./cmd/midiMon -in <port-substr>
```

`-in`/`-out` match a case-insensitive substring of the device label
(first match wins), same as the Java tools. Use `-list` /
`cmd/audioInfo` / `cmd/midiInfo` to discover the substrings.

## Render parity & the regression harness

The committed reference summaries in `regression/fixtures/summary/` were
rendered by the **Java** engine. The Go port reproduces all 49 fixtures
in `regression/renders.lst` within the tolerances in
`regression/diff_summaries.py`.

- `go test ./regression` renders every fixture in-process through the Go
  engine and diffs against the Java summaries. This is the parity guard;
  run it after any engine change.
- `regression/check.sh --go` does the same from the shell (building
  `cmd/p`); it uses the committed MIDI fixtures and needs no JDK. Without
  `--go` it re-renders with the Java jar for cross-checking.

The Java engine is not bit-reproducible across runs (oscillator phase
jitter), so comparison is summary-statistic based (peak, RMS, RMS
envelope, zero-frame %), not sample-for-sample.

## Engine architecture notes (Go-specific)

- **Pull-driven graph.** `synth.Wire` is the Go equivalent of Java's
  `WiredIntSupplier`: each wire latches its value on first read per sample
  and is reset every sample (`Voice.ResetWires`), so self-feeding (FM)
  graphs don't loop. `Synth.Step` is the hot path — no allocation.

- **Multi-timbral.** Voices are keyed by `(channel, note)` (see
  `voiceKey` in `synth/synth.go`), so the same note on two channels is two
  voices. Each channel plays its own patch: `Synth.defaultPatch` covers
  unassigned channels, `SetChannelPatch` overrides per channel. `cmd/p`
  uses a single default patch; `cmd/o` exposes per-channel assignment via
  repeatable `-patch chan:name`.

- **Phase-clock lifecycle.** Oscillator phase clocks live on the shared
  `Instant` and are ticked every sample. Components allocate them through
  `Voice.AddPhaseClock` so the voice can release them
  (`Instant.RemoveClock`) when it is torn down. Without this the tick list
  would grow for every note ever played — invisible in short offline
  renders, a steady audio-thread slowdown for a live session.

- **Live threading (`cmd/o`).** The engine is single-threaded and owned
  exclusively by the audio callback. The MIDI callback runs on a different
  thread and never touches the engine directly: it pushes note/CC commands
  onto a buffered channel with a non-blocking send (dropping under
  overflow rather than blocking the MIDI driver). The audio callback
  drains that channel at the top of each buffer, then renders the buffer's
  samples. No lock sits on the per-sample path. This mirrors Java's
  `MidiListenerThread` queue, drained on the audio thread. `patch.Apply`
  (voice construction) therefore runs on the audio thread during the
  drain; it is comfortably sub-buffer for normal patches at human
  note-rates.

## Known gaps / deferred

- **Voice pool.** Java pre-creates a per-channel voice pool to avoid GC
  pauses. The Go port allocates a voice graph per note instead. A
  graph-recycling pool was deliberately deferred: it needs an exact
  per-component `Reset()` (env/echo/filter/smooth/wave) that risks the
  verified render parity for a GC-only benefit, now that the concrete live
  resource bug (the phase-clock leak above) is fixed. If profiling later
  shows GC pauses under sustained live play, the safe approach is a
  `Resettable` interface with fresh-allocation fallback.
- **Wave editor GUI** is not ported.
- **SMPTE-timed** MIDI files are rejected (`midi.ReadFile`); only metric
  (ticks-per-quarter) timing is supported.
- **Sample rate** defaults to 44.1 kHz (the regression-tested path);
  `cmd/o -sample-rate` can override it.
