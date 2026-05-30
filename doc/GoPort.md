# The Go port

OndeSynth has a Go port of the synth engine and tooling alongside the
original Java. The Go module is rooted at the repo top level (`module
ondes` in `go.mod`); the Java tree under `src/main/java` is unchanged and
remains the reference implementation.

## Why this port exists

The primary goal of the port is **to eliminate the GC-induced audio
dropouts the Java version suffered during live performance.** On the JVM,
allocating a voice graph on note-on could trigger a stop-the-world
collection — tens to hundreds of milliseconds with every thread halted —
which on the real-time audio thread means a missed buffer and an audible
click. Render parity with Java (below) is a correctness *guard*, but
clean live playback is the *point*.

This is why the live path matters as much as the render path, and why the
voice-pool question (see *Known gaps*) is treated as a real engineering
decision rather than optional polish.

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

- **Voice pool — deferred pending measurement.** Java pre-creates a
  per-channel voice pool so no allocation happens on the audio thread. The
  Go port allocates a fresh voice graph per note (`newVoice` in
  `synth/voice.go`) instead. A graph-recycling pool was deferred — but the
  reasoning is *not* "the GC benefit is trivial," because avoiding live GC
  dropouts is this port's whole purpose (see *Why this port exists*). The
  actual reasoning:

  1. **Go's collector likely already solves the Java problem.** Go's GC is
     concurrent with sub-millisecond stop-the-world pauses by construction,
     so the per-note allocation pattern that caused multi-millisecond STW
     stalls on the JVM may never miss a buffer deadline in Go. If so, the
     port already meets its goal and a pool is unnecessary complexity.
     **This is unverified and should be measured before any pool is built**
     (see below).
  2. **A pool's cost is a real parity risk.** Recycling a voice graph
     requires an exact per-component `Reset()` (env carries ~10 state
     fields plus segment timing; echo a delay buffer; filters their
     history; wave its phase). Any missed field silently carries stale
     time-domain state — residual oscillator phase, an envelope timer
     measured against the never-resetting `Instant.sample` counter, an
     uncleared delay/filter tail — into the next note, shifting the signal
     in time and breaking parity with no error. Crucially, the regression
     suite calls `synth.New` fresh per fixture, so a buggy pool could pass
     49/49 and only corrupt audio under sustained live play.

  **Decision sequence if dropouts are ever observed in `cmd/o`:**

  1. *Measure first.* Run `cmd/o` under realistic heavy play (fast
     arpeggios, chord stacks) at the buffer size actually used, with
     `GODEBUG=gctrace=1`, and count audio-callback underruns. Small buffers
     (`-buffer-size 256` ≈ 5.8 ms/block) are where any residual GC assist
     would show; the 1024 default has ~23 ms of slack. Zero underruns =
     the port already meets its goal and no pool is needed.
  2. *If it still drops out, make the pool parity-safe before building it.*
     Extend the regression harness with a mode that renders multiple
     fixtures/notes through **one reused engine** to force slot recycling,
     diffing against fresh-allocation output. Bit-identical across all 49
     fixtures proves the reset is correct against the trusted baseline and
     turns a silent reset bug into a deterministic CI failure.
  3. *Bound the blast radius.* Implement recycling behind a `Resettable`
     interface with fresh-allocation fallback: reuse a voice slot only when
     every component in its graph implements `Reset`, otherwise allocate
     fresh.

  Note that some of the Java dropouts may also have been the JavaSound MME
  audio path rather than GC; `cmd/o`'s malgo/CoreAudio path sidesteps that
  independently.
- **Wave editor GUI** is not ported.
- **SMPTE-timed** MIDI files are rejected (`midi.ReadFile`); only metric
  (ticks-per-quarter) timing is supported.
- **Sample rate** defaults to 44.1 kHz (the regression-tested path);
  `cmd/o -sample-rate` can override it.
