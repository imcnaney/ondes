# Timings — per-note voice-setup cost (Go port)

This page records the measured cost of setting up a voice on note-on in the
Go port, and what it means against the audio-buffer deadline. It exists to
settle the recurring "do we need a pre-built voice pool?" question with a
number instead of an argument. See also the *Voice pool* discussion in
[GoPort.md](GoPort.md).

## What is measured

In a fully modular synth, every note must instantiate and wire the components
of its patch before it can sound. In the Go port that work runs on the audio
thread at note-on: `newVoice` followed by `patch.Apply` (which calls `Make` +
`Configure` on every component and resolves the `out:` wiring). This is
exactly the cost Java's `ChannelVoicePool` pre-pays at startup.

`BenchmarkNoteOnSetup` in `regression/setup_bench_test.go` times a cold
`NoteOn` (fresh `Synth` per iteration, so no retrigger fast-path) across a
light-to-heavy spread of patches, and reports allocations.

Re-run after any change touching `Configure` or the wire graph:

```
go test ./regression -run=^$ -bench=NoteOnSetup -benchmem
```

## Results

Measured on the dev machine (Apple Silicon, macOS, 44.1 kHz). Absolute numbers
vary by host; the ratios and the headroom against the buffer deadline are the
point.

| Patch | Setup / note | Allocs | Bytes/note | % of 256-buf (5.8 ms) | % of 1024-buf (23 ms) |
|---|---|---|---|---|---|
| sine | ~0.8 µs | 24 | 1.4 KB | 0.014% | 0.004% |
| saw | ~0.8 µs | 24 | 1.4 KB | 0.014% | 0.004% |
| bassoon | ~1.0 µs | 29 | 1.6 KB | 0.018% | 0.004% |
| bell-organ | ~1.4 µs | 34 | 1.8 KB | 0.025% | 0.006% |
| brass | ~5.5 µs | 120 | 6.8 KB | 0.09% | 0.024% |
| **ocean2** (heaviest fixture) | **~25.5 µs** | 113 | 18.8 KB | **0.44%** | **0.11%** |

Heaviness tracks harmonic/anharmonic partial count (anharmonic allocates one
phase clock per partial) and filter-coefficient setup, not raw component
count.

## Interpretation

- The audio callback must produce one buffer of samples before its deadline.
  At 44.1 kHz a 256-sample buffer is **5.8 ms**; the 1024-sample default is
  **23 ms**.
- The heaviest real patch costs **~25 µs** to set up — **0.44%** of the
  smallest buffer anyone would run, **0.11%** of the default.
- A 10-note chord all landing in one buffer is still only **~4%** of a
  256-sample buffer (**~1%** of the default).
- It would take a patch roughly **200× heavier than any in the suite**, or a
  very large simultaneous chord, before setup threatened even the smallest
  buffer.

**Conclusion:** the per-note configuration cost a voice pool exists to
amortize is two to three orders of magnitude below a single buffer in Go.
That is why the recycling pool is deferred — not because the cost is
philosophically trivial, but because it is *measured* and small. The pool's
parity risk, by contrast, is real; see [GoPort.md](GoPort.md).

> Not to be confused with [timing.md](timing.md), which documents the
> `-log-main-out` per-sample update-loop timing log.
