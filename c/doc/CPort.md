# The C port

OndeSynth has a C port of the synth engine alongside the original Java and
the [Go port](../../doc/GoPort.md). It lives entirely under `c/` and is
additive: the Java tree (`src/main/java`) remains the reference
implementation, and the Go tree is untouched. The C port follows the Go
port's architecture decision-for-decision (the Go port already made every
Java→native call once); its correctness bar is the **same committed
Java-rendered regression summaries** the Go port is held to.

## Why this port exists

Same motivation as the Go port: **eliminate the JVM GC-induced audio
dropouts the Java version suffered during live play.** C has no GC at all,
so the per-note voice graph is built and torn down deterministically. As
with Go, render parity with Java is the correctness *guard*; clean live
playback is the *point* (the live path is a later phase — see *Scope*).

## Scope

| Capability | Java | Go | C |
|---|---|---|---|
| MIDI file → WAV render | `ondes.file.PlayMidiFile` (`./p`) | `cmd/p` | `c/build/p` — **at parity** |
| Live MIDI → audio | `ondes.App` (`./o`) | `cmd/o` | `c/build/o` — miniaudio + CoreMIDI |
| Device list (`-list`) | yes | yes | built into `o -list` |
| Standalone device/monitor tools | yes | yes | — folded into `o -list` |
| Wave editor GUI | `ondes.tools.WaveEditor` | — | — not ported |

All ten registered component types are implemented (`wave`, `filter`,
`env`, `mix`/`dynamic-mix`, `balancer`, `op-amp`, `controller`, `smooth`,
`midi-note`, `echo`). As in the Go port, `limiter` is the global main-mix
limiter (`src/synth/limiter.c`), not a patch component.

## Build & run

The C port uses CMake. The **offline path has no third-party
dependencies** — it is plain C (a self-contained block-YAML parser,
hand-written WAV writer and SMF reader). The **live path needs no
installed libraries either**: audio output is the vendored single-header
[miniaudio](https://miniaud.io/) (driving CoreAudio) and MIDI input is
CoreMIDI, a macOS system framework.

```
cmake -S c -B c/build && cmake --build c/build
```

### Offline render (`p`, any platform)

```
# render a MIDI file through a patch to WAV (run from the repo root so
# ./program and src/main/resources/program resolve):
c/build/p -patch <name> in.mid out.wav
c/build/p -patch sine regression/fixtures/mid/scale.mid /tmp/sine.wav
c/build/p -pool -patch sine in.mid out.wav   # recycle voice graphs
```

### Live play (`o`, macOS)

```
c/build/o -list                          # list audio outputs + MIDI inputs
c/build/o -in <port-substr> -patch <name>    # play live from a keyboard
c/build/o -in <substr> -out <substr> -buffer-size 256 -patch brass
c/build/o -in <substr> -hold -patch sine     # suppress note-offs (drone)
c/build/o -patch 2:brass -patch sine         # multi-timbral: ch-2 + default
c/build/o -selftest -patch ocean2            # play a note, no MIDI in (diag)
c/build/o -in <substr> -pool -patch brass    # recycle voice graphs
```

`-patch` accepts the same name forms as the Java/Go loaders (exact
basename first, then a case-insensitive substring match, e.g.
`-patch program/bell`); for `p`, `-tail` / `-max-tail` mirror `cmd/p`.
`-in` / `-out` match a case-insensitive substring of the device label.

## Render parity & the regression harness

`regression/check_c.sh` renders every fixture in `regression/renders.lst`
through `c/build/p` and diffs each WAV against the committed **Java**
reference summary in `regression/fixtures/summary/` via
`regression/diff_summaries.py` (the same tolerance-based peak/RMS/RMS-
envelope/zero-frame comparison the Go harness uses — Java is not
bit-reproducible across runs). The C port reproduces **all 49 fixtures**.

Beyond the suite, the C port has **full behavioral parity with the Go
port across all 109 patches**: the same 98 produce sound and the same 11
are silent (unsupported `sweep-sinc`/`wave-editor` shapes and env
`preset:`, a `sin` typo in `four-sour`, a `description:` key with no
`type`, and a multi-target `out: lpf2, main` in `test-echo2` — each fails
identically under both ports).

## Engine architecture notes (C-specific)

The signal-flow model is identical to Java/Go; only the realization in C
differs. See [GoPort.md](../../doc/GoPort.md) for the shared decisions
(pull-driven graph, per-`(channel,note)` voices, flattened component
context, phase-clock lifecycle, CC replay, draining tails). The C-specific
choices:

- **Pull-driven graph.** `Wire` (`include/ondes/wire.h`) is the C
  equivalent of Java's `WiredIntSupplier` / Go's `synth.Wire`: a
  `double (*compute)(void *ctx)` plus a latched value and a per-sample
  `visited` flag, so self-feeding (FM) graphs don't recurse.
  `synth_step` is the hot path and allocates nothing.

- **Per-voice arena.** Each `Voice` owns one arena allocator
  (`include/ondes/arena.h`); every component, wire, junction and per-voice
  buffer (echo tape, filter history, harmonic stacks) is allocated from
  it, so voice teardown is a single `arena_free` — no per-object walk, no
  fragmentation on the audio path. Growable per-voice vectors use
  `arena_grow` (append-once, ~2× overhead, all reclaimed at teardown).
  Phase clocks are the one exception: they are owned by the shared
  `Instant` and explicitly released (`voice_release_clocks`) before the
  arena is freed, so the tick list stays bounded to live voices.

- **Components are vtables.** `Component` (`include/ondes/component.h`) is
  a struct of function pointers: `configure`/`output` are required;
  `add_input` (Inputter), `on_midi` (MidiListener) and `named_output`
  (linear-out/log-out/out-level) are optional — a NULL slot means "not
  supported", the exact semantics of a failed Go interface assertion.
  Concrete components embed `Component base;` as their first member.
  A small registry (`src/component/registry.c`) maps the YAML `type`
  string to a constructor; `component_register_all()` is the C analogue of
  the Go port's blank imports and must run before patches load.

- **Float signal path.** Samples are `double` end-to-end (matching the Go
  port), converted to int16 only in the WAV writer, so the committed
  parity summaries apply unchanged.

- **Live threading.** `cmd/o` mirrors Java's `MidiListenerThread` and the
  Go port: the engine is single-threaded and owned by the miniaudio render
  callback. The CoreMIDI callback (a separate thread) parses the raw byte
  stream — handling running status — and pushes note/CC commands onto a
  lock-free single-producer/single-consumer ring (C11 `stdatomic`), which
  the audio callback drains at the top of each period before rendering. No
  lock sits on the per-sample path; on overflow the MIDI side drops and
  counts rather than blocking. The audio (`src/audiodev/`) and MIDI
  (`src/mididev/`) layers are excluded from the offline `libondes`, so the
  renderer stays dependency-free and the live deps are linked only into
  `o`.

- **Voice pool (opt-in, `-pool`).** Off by default — the proven path
  builds and frees a voice graph per note. With `synth_set_pool_enabled`
  (the `-pool` flag on `p`/`o`), a finished voice is reset and returned to
  a per-patch idle pool instead of being freed, so note-on reuses a
  pre-built graph rather than re-running `patch.Apply` on the audio thread
  (the work the Java `ChannelVoicePool` exists to pre-pay).

  The hard part of any pool is the **reset**: recycling a graph demands
  that every component's time-domain state (envelope timers, filter and
  echo history, oscillator phase, …) return exactly to its post-build
  value, and a single missed field silently corrupts audio while still
  passing a suite that allocates fresh per fixture. This port sidesteps
  the per-field risk structurally: because all per-voice mutable state
  lives in the voice arena, reset is an **`arena_snapshot` taken after
  `Apply` and an `arena_restore` on reuse** — a byte-exact rewind of the
  whole graph (pointers stay valid, the arena isn't moved), so no field
  can be forgotten. The only per-voice state outside the arena is the
  oscillator phase clocks (owned by the shared `Instant`); the one
  component that owns them, `wave`, implements the optional `reset` vtable
  slot to re-zero them. Pooled voices keep their clocks registered while
  idle (bounded by pool size) and reset them on reacquire.

  Recycling is held to a hard guard: `regression/check_c_pool.sh` renders
  every fixture both pooled and fresh; the 46 deterministic fixtures must
  be **byte-for-byte identical** (a reset bug shifts the signal and shows
  as a diff), the 3 noise fixtures are summary-checked, and the pooled
  renders must still pass 49/49 against the Java summaries. Measured reuse:
  `sine`/scale recycles one voice across all 8 notes (8×) with bit-identical
  output; overlapping notes (`brass`, `ocean2`) correctly grow the pool.

- **Self-contained YAML.** `src/patch/yaml_spec.c` is a focused
  block-style YAML parser for the patch subset actually used by the corpus
  (block maps and sequences — including sequences aligned with their key —
  multi-token scalar items, nested maps, inline `#` comments, empty
  items). Flow collections, quotes, anchors/aliases and block scalars are
  intentionally unsupported; none appear in any of the 109 patch files.

## Verification

- **Unit tests** (`ctest --test-dir c/build`): wire latch/reset, instant
  phase-clock lifecycle, arena, and voice keying-by-`(channel,note)` with
  clock release on teardown.
- **Parity:** `regression/check_c.sh` → 49/49.
- **Memory:** ASan + UBSan renders of the heaviest/polyphonic fixtures
  (`ocean2`, `bell-organ` chord, `brass`, `repeater`) are clean; macOS
  `leaks` reports 0 leaks (LSan is unavailable on Apple clang).
- **Voice pool:** `regression/check_c_pool.sh` → 46/46 deterministic
  fixtures bit-identical pooled-vs-fresh, 3 noise fixtures summary-checked,
  and pooled renders pass 49/49 vs Java. ASan/UBSan clean and 0 leaks with
  `-pool`. A unit test asserts a graph is reused (pool size 1) across
  repeated notes.
- **Live path:** `o -selftest` drives a note through the exact live render
  path (ring → `render` → engine) and reports the output peak — `sine`
  matches the offline render (0.0195), `brass`/`ocean2` scale up as
  expected. The CoreMIDI input layer is verified by an in-process
  virtual-source loopback (note-on, CC, running-status and note-off all
  parse correctly).

## Known gaps / deferred

- **Live path is macOS-only.** Audio (miniaudio) is cross-platform, but
  MIDI input uses CoreMIDI directly. A Linux/Windows build would add an
  ALSA/WinMM or RtMidi backend behind the existing `mididev.h` interface.
  Live play has been exercised on macOS/CoreAudio only.
- **Wave editor GUI** — not ported.
- **SMPTE-timed** MIDI files are rejected; only metric (ticks-per-quarter)
  timing is supported.
- **Sample rate** is 44.1 kHz (the regression-tested path).
