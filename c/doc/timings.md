# C port — per-note voice-setup timings

Per-note voice setup for the C port. Reproduce with:

```
cmake --build c/build --target note_setup
c/build/note_setup            # run from the repo root so patches resolve
```

The benchmark times two paths per patch (20 000 iterations each):

- **fresh** — `voice_new` + `patch.Apply`, i.e. building a voice graph from
  scratch (what a note-on costs without pooling), matching the Go port's
  `BenchmarkNoteOnSetup`.
- **pool** — `voice_reset_for_reuse`, the arena-snapshot restore + phase-clock
  reset a pooled note-on does instead of rebuilding.

## Results (Apple Silicon, -O2)

The last column records the now-removed Go port's `BenchmarkNoteOnSetup`
figures, kept for the comparison: the C arena makes the fresh build cheaper
than Go's (dramatically so for heavy patches), before the pool is even
considered.

| patch | fresh ns/op | pool ns/op | speedup | (Go fresh, removed) |
|---|---:|---:|---:|---:|
| sine | ~686 | ~66 | ~10× | ~846 |
| saw | ~512 | ~47 | ~11× | ~841 |
| bassoon | ~599 | ~41 | ~15× | ~1042 |
| bell-organ | ~847 | ~42 | ~20× | ~1454 |
| brass | ~3745 | ~76 | ~49× | ~5463 |
| ocean2 (heaviest) | ~3133 | ~82 | ~38× | ~25552 |

## Reading

- **The C fresh path is already cheap** — and markedly cheaper than Go for
  heavy patches: `ocean2` builds in ~3.1 µs vs Go's ~25.6 µs. The per-voice
  arena turns the dozens of small allocations Go makes (and the GC pressure
  they create) into a couple of bump allocations, so even *without* the pool
  the work the Java `ChannelVoicePool` exists to amortize is well under 1% of
  any audio buffer (a 256-frame buffer is ~5.8 ms).
- **The pool flattens setup to ~40–85 ns**, essentially independent of patch
  complexity — it's an arena `memcpy` plus zeroing a few phase clocks, not a
  graph rebuild. That is the constant-time, allocation-free note-on the pool
  was built to provide; the speedup grows with patch size (≈50× for `brass`)
  precisely because the rebuild it replaces grows while the reset does not.

## Caveat (unchanged from the Go analysis)

Even the fresh path costs two to three orders of magnitude less than a single
buffer, so the pool is a **latency optimization, not a correctness need** — it
is opt-in (`-pool`) for that reason. Its value is bounding worst-case note-on
jitter (dense chords on the heaviest patches) and removing all audio-thread
allocation; see the parity guard in `regression/check_c_pool.sh` and the pool
notes in [CPort.md](CPort.md).

# Render throughput (per-sample hot path)

Cost of `synth_step` under a held chord — the number that decides how much
polyphony fits in real time. Reproduce with:

```
cmake --build c/build --target render_throughput
c/build/render_throughput <patch> <voices>
```

Two engine optimizations were applied and measured (both preserve parity —
49/49 vs Java and bit-identical pooled-vs-fresh):

1. **Generation-counter wire invalidation.** The per-sample "reset every
   wire" pass was replaced by a single global generation bump
   (`wire_advance_gen`); a wire is current iff its stamp matches. Output is
   bit-identical; modest gain since the hot path is compute-bound.
2. **Sine lookup table.** Oscillators called libm `sin()` once per partial
   per voice per sample; a 4096-entry table + linear interpolation (restoring
   the Java port's `SineLookup`) replaces it. This is the big lever for the
   oscillator-heavy patches that dominate the corpus.

## Results (Apple Silicon, -O3, ns per sample)

| patch / voices | before | after | speedup | after = realtime |
|---|---:|---:|---:|---:|
| sine ×1 | 45.6 | 19.9 | 2.3× | 1141× |
| sine ×32 | 561 | 243 | 2.3× | 93× |
| bell-organ ×8 | 691 | 166 | 4.2× | 137× |
| bell-organ ×32 (worst) | 2991 | 757 | 3.9× | **30×** |
| brass ×8 | 464 | 370 | 1.3× | 61× |
| ocean2 ×8 | 795 | 656 | 1.2× | 35× |

Oscillator patches gain 2–4×; filter/noise-heavy patches (`ocean2`, `brass`)
gain less because they were never `sin()`-bound. The worst case in the suite —
32 simultaneous 5-partial harmonic voices — went from 8× to 30× real time, i.e.
~3% of one core. `-mcpu=native` was measured and made no difference (the hot
path is `sin()`/pointer-chasing, not autovectorizable arithmetic).
