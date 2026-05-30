# Plan: Finish the Java→Go port of OndeSynth

## Context

OndeSynth is a modular software synthesizer originally written in Java. A port
to Go (branch `golang`) was started and "failed part way through." An audit of
the current state shows the port is **much further along than expected**:

- The **offline render path is complete and at parity.** `cmd/p` loads a patch
  (`patch.Load`), reads a MIDI file (`midi.ReadFile`), drives the real engine
  (`synth.Synth`) sample-by-sample, and writes a WAV (`audio.WriteMono16`).
  Rendering all **49 regression fixtures** in `regression/renders.lst` with the
  Go binary and diffing against the committed **Java** reference summaries
  (`regression/diff_summaries.py`) yields **49/49 PASS**.
- All **11 patch component types** are implemented and registered: wave, filter
  (sinc/iir/biquad), env, mix, dynamic-mix, balancer, limiter (engine-level),
  op-amp, controller, smooth, midi-note, echo.
- The engine core is ported: `Synth`, `Voice`, `Wire` (the per-sample latching
  `WiredIntSupplier`), `Junction`, `Instant`/`PhaseClock`, `Limiter`.

What "failed part way through" actually refers to is the **live-performance path
and tooling**, which were never wired to the real engine:

- The Java `App`/`SynthSession`/`MidiListenerThread`/`MonoMainMix` (the `o`
  script: listen on a MIDI input, play live through a patch to an audio output)
  exists in Go only as a throwaway proof-of-concept (`cmd/spike`) with its own
  sine synth — it does **not** use `synth.Synth`.
- The device-info CLIs (`audioInfo`, `midiInfo`, `midiMon`) are not ported.
- There is **no Go-side regression harness**: `regression/check.sh` re-renders
  with the **Java** jar, so parity is verified by hand, not in CI.
- The engine is single-patch and keys voices by **note only** (`map[uint8]*Voice`),
  so it cannot host more than one MIDI channel/patch (Java is 16-channel
  multi-timbral with a per-channel voice pool).

This plan completes three things (the wave-editor GUI is **out of scope**):
1. The **live synth app** (`cmd/o`) + device-info CLIs.
2. A **Go regression harness** to lock parity into CI.
3. **Multi-timbral + voice-pool** hardening of the engine.

The intended outcome: a Go build that can both render MIDI files to WAV (already
working) **and** be played live from a MIDI keyboard through any loaded patch,
with parity guarded automatically.

---

## Phase 1 — Go regression harness (lock in the parity we already have)

Do this first: it codifies the 49/49 result as a guard so later engine changes
(Phase 3) can't silently regress.

- [x] **Add a Go-driven regression test** `regression/regression_test.go` (new
      `package regression`) that, for every row in `renders.lst`, renders with
      the in-process engine (same loop as `cmd/p/main.go`: `patch.Load` →
      `midi.ReadFile` → `synth.New` → `Step` loop → `audio.WriteMono16` into a
      `t.TempDir()`), then shells out to `python3 diff_summaries.py` once against
      the committed `fixtures/summary`. Parse `renders.lst` with a small splitter
      (skip blank/`#` lines). Skip with `t.Skip` if `python3` is unavailable.
- [x] **Add a Go renderer entry to `check.sh`** (or a sibling `check-go.sh`): a
      `--go` mode that builds `./cmd/p` and renders each fixture with the Go
      binary into the tempdir instead of the Java jar, reusing the existing
      `diff_summaries.py` step. Keep the Java path for cross-checking.
- [x] **Remove/curate the 3 heuristic tests** in `cmd/p/render_test.go` once the
      full-suite test exists, or keep them as fast smoke tests — they no longer
      need to carry parity weight.
- [x] Verify: `go test ./regression/` is green (49/49), and `go test ./...`
      passes.

---

## Phase 2 — Live synth app (`cmd/o`) + device CLIs

### 2a. Extract reusable device helpers from `cmd/spike`

`cmd/spike/main.go` already proves malgo (audio out) + gomidi/rtmidi (MIDI in)
on macOS. Lift its helpers into the existing top-level packages so `cmd/o` and
the info CLIs can share them. **Return errors instead of calling `die`/`os.Exit`**
so they are library-grade.

- [x] **`audio/device.go`** (new, in existing `audio` package): `SelectOutputDevice(ctx, substr) (*malgo.DeviceInfo, label string, err error)` and `ListOutputDevices(ctx) ([]malgo.DeviceInfo, error)` — adapted from spike's `selectOutputDevice`/`listDevices`. Pulls the malgo import into `audio`.
- [x] **`midi/port.go`** (new, in existing `midi` package): `FindInPort(substr) (drivers.In, error)` and `ListInPorts() []string` — adapted from spike's `findInPort`/the MIDI half of `listDevices`. **Keep the `_ "...rtmididrv"` blank import OUT of the library** (it pulls CGo/rtmidi) — register the driver only in the live `cmd/*` mains, so the offline `cmd/p` build stays CGo-free.

### 2b. `cmd/o/main.go` — the live instrument

Mirrors the Java `o` / `ondes-args` conventions and reuses the offline wiring.

- [x] **Flags:** `-in <substr>` (MIDI port), `-out <substr>` (audio device),
      `-patch <name>` (default `sine`; also accept a single loose positional as
      the patch name), `-buffer-size <n>` (period in frames, default 1024),
      `-hold` (suppress note-offs / drone), `-list` (list MIDI ins + audio outs
      and exit), `-sample-rate <n>` (default `audio.SampleRate` = 44100 to stay
      on the regression-tested engine path).
- [x] **Component registration:** copy the blank-import block from
      `cmd/p/main.go` (`_ "ondes/component/..."` ×10) **and** the rtmidi driver
      import. Missing these is a silent-failure trap — every NoteOn would fail
      `patch.Apply`. Watch for the engine's one-time "patch failed to apply" log.
- [x] **Threading = command queue, NOT a mutex.** The engine is single-threaded
      and stays that way. Define `type command struct{ kind uint8; ch, d1, d2 uint8 }`
      and a buffered `chan command` (≈1024). The **gomidi callback** does a
      non-blocking send (`select { case q <- c: default: /* drop, count overflow */ }`)
      — it must never block the MIDI driver thread; under `-hold` it simply
      doesn't enqueue note-offs. The **malgo Data callback** drains the whole
      queue at the top of each buffer (applying `NoteOn`/`NoteOff`/`ControlChange`
      to the engine it exclusively owns), then runs the per-sample loop. This
      keeps all engine mutation on one thread with no lock on the hot path; the
      drain cost is amortized per ~1024-frame buffer, not per sample. (This
      mirrors Java's `MidiListenerThread` `ConcurrentLinkedQueue`, just drained
      on the audio thread.)
- [x] **Audio bridge:** in the Data callback, after draining, write stereo F32
      from the engine's mono `float64`: `s := float32(eng.Step()); out[2*i]=s; out[2*i+1]=s`
      (reuse spike's `unsafe.Slice` cast). The engine's `Limiter` already bounds
      output to [-1,1], so no extra clamp. Config: `FormatF32`, `Channels=2`,
      `SampleRate`, `PeriodSizeInFrames=buffer`.
- [x] **Lifecycle:** init malgo context, select output device, `patch.Load`,
      `synth.New`, `FindInPort` + `midi.ListenTo`, `dev.Start()`, wait on SIGINT
      (model on spike lines 156–176), clean teardown.
- [x] **Note** the known single-patch limitation in a comment (two channels
      playing the same note number collide while keyed by note) — resolved in
      Phase 3.

### 2c. Device-info CLIs (thin wrappers over 2a)

- [x] **`cmd/audioInfo/main.go`** — malgo context + `audio.ListOutputDevices`, print name + default flag.
- [x] **`cmd/midiInfo/main.go`** — `midi.ListInPorts` (+ rtmidi driver import), print ports.
- [x] **`cmd/midiMon/main.go`** — `FindInPort` + `midi.ListenTo` printing every parsed message (note on/off/CC via `GetNoteStart`/`GetNoteEnd`, plus generic `msg.String()` for CC/pitchbend) until SIGINT.

### 2d. Cleanup

- [x] Once `cmd/o` is validated live, **delete `cmd/spike`** (or refactor it to
      call the shared helpers) — it was the proof-of-concept this replaces.

---

## Phase 3 — Multi-timbral + voice-pool hardening

This is the larger engine change. Current `Synth` holds one `patch` and keys
`voices` by `note`. Goal: per-MIDI-channel patches and a voice pool to cut GC
churn under sustained play. **Do this after Phase 1's harness is green** so every
step is checked against 49/49.

- [x] **Re-key voices by `(channel, note)`** in `synth/synth.go`: `voices` is now
      `map[uint16]*Voice` keyed by `voiceKey(ch, note)`. `NoteOn`/`NoteOff`/
      `QueueNoteEnd`/`removeVoice`/`Step` all updated. Single-channel offline
      fixtures unchanged (channel 0 ⇒ key == note). Covered by
      `TestVoiceKeyingByChannelAndNote`.
- [x] **Per-channel patch:** `Synth` now holds `defaultPatch` + `patches
      map[uint8]Patch` with `SetChannelPatch`/`patchFor`. `cmd/o`'s `-patch` is
      repeatable and accepts `name` (default) or `chan:name` (channel 1-16).
      `cmd/p` keeps single-patch behavior (passes one default). Covered by
      `TestChannelPatchOverride` / `TestNilDefaultPatchDropsNote`.
- [x] **`ControlChange` fan-out** now targets only voices on the matching channel.
      CC-driven fixtures (smooth, balancer) still pass.
- [x] **Phase-clock lifecycle (live-leak fix, added during Phase 3):** the shared
      `Instant.clocks` list grew unbounded — every note ever played kept ticking
      forever (harmless for short offline renders, a steady audio-thread slowdown
      for live `cmd/o`). Clocks are now created via `Voice.AddPhaseClock`, tracked
      per voice, and released via `Instant.RemoveClock` when the voice is removed.
      Covered by `TestPhaseClocksReleasedWithVoice`. This was the concrete resource
      problem Java's voice pool exists to avoid.
- [~] **Voice pool — DEFERRED (deliberate).** A graph-recycling pool that reuses
      voice component graphs to cut GC churn requires an exact per-component
      `Reset()` across env/echo/filter/smooth/wave (env alone carries ~10 state
      fields plus per-step timing). Any imperfect reset silently corrupts audio
      and would break the verified 49/49 parity, for a GC-only benefit now that
      the real live resource bug (the clock leak above) is fixed. Go handles
      per-note allocation comfortably at human note-rates. Revisit only if
      profiling shows GC pauses under sustained live play; the safe path is a
      `Resettable` interface with fresh-allocation fallback (recycle a voice only
      when every component supports `Reset`).
- [x] Verify: `go test ./...` green (regression stays 49/49, new synth unit
      tests pass). Multi-channel live spot-check via `cmd/o` with per-channel
      `-patch` assignments remains a manual step (needs a MIDI controller).

---

## Files at a glance

**Create:** `regression/regression_test.go`, `audio/device.go`, `midi/port.go`,
`cmd/o/main.go`, `cmd/audioInfo/main.go`, `cmd/midiInfo/main.go`,
`cmd/midiMon/main.go`, (Phase 3) `synth/voicepool.go`.

**Modify:** `regression/check.sh` (add Go mode), `cmd/p/render_test.go` (trim
heuristics), `synth/synth.go` (re-key + per-channel patch + pool integration),
`cmd/o`/`cmd/p` patch-assignment wiring.

**Reuse:** `cmd/spike/main.go` (helper source), `cmd/p/main.go` (component
import block + render loop), `patch.Load`, `midi.ReadFile`, `audio.WriteMono16`,
`audio.SampleRate`, `regression/diff_summaries.py`, `synth.New`/`NoteOn`/
`NoteOff`/`ControlChange`/`Step`.

**Out of scope:** the Java Swing `WaveEditor` (`w` script) and its `tools/` package.

---

## Verification

- **Parity (automated):** `go test ./regression/` renders all 49 fixtures with
  the Go engine and diffs vs the committed Java summaries — must report 49/49.
  Run after every Phase 3 change. `go test ./...` and `go build ./...` must pass.
- **Offline still works:** `go run ./cmd/p -patch <name> regression/fixtures/mid/scale.mid /tmp/out.wav`.
- **Live (manual, needs a MIDI controller + audio out):**
  - `go run ./cmd/o -list` lists MIDI inputs and audio outputs.
  - `go run ./cmd/o -in <controller-substr> -out <device-substr> -patch sine`
    plays notes live; try other patches (`saw`, `brass`, `ocean2`).
  - `-hold` sustains/drones (note-offs suppressed); `-buffer-size 256` lowers
    latency, `2048` raises stability.
  - `go run ./cmd/audioInfo`, `./cmd/midiInfo`, `./cmd/midiMon` enumerate/monitor
    devices.
- **Multi-timbral (Phase 3):** set a controller to channel 1 vs 2 with different
  `-patch` assignments and confirm independent timbres; render a multi-channel
  MIDI file through `cmd/p` and confirm no note-collision dropouts.

## First execution step

Since the user asked for the plan "in a file `plan.MD`", begin execution by
copying this plan into the repo as `plan.MD` (with the checkboxes) so progress is
trackable from the working tree, then proceed with Phase 1.
