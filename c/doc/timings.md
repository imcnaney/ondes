# C port — per-note voice-setup timings

Per-note voice setup for the C port, the counterpart of the Go port's
[timings.md](../../doc/timings.md). Reproduce with:

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

| patch | fresh ns/op | pool ns/op | speedup | Go fresh ns/op |
|---|---:|---:|---:|---:|
| sine | ~686 | ~66 | ~10× | ~846 |
| saw | ~512 | ~47 | ~11× | ~841 |
| bassoon | ~599 | ~41 | ~15× | ~1042 |
| bell-organ | ~847 | ~42 | ~20× | ~1454 |
| brass | ~3745 | ~76 | ~49× | ~5463 |
| ocean2 (heaviest) | ~3133 | ~82 | ~38× | ~25552 |

(Go figures from `go test ./regression -run='^$' -bench=NoteOnSetup -benchmem`.)

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
