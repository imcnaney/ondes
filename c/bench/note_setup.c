// note_setup benchmarks per-note voice setup, the C counterpart of the Go
// port's BenchmarkNoteOnSetup (regression/setup_bench_test.go). For each
// patch it times two paths:
//
//   fresh - voice_new + patch.Apply (build a graph from scratch), the work
//           that runs on the audio thread at note-on without pooling;
//   pool  - voice_reset_for_reuse (arena snapshot restore + clock reset),
//           the work a pooled note-on does instead.
//
// Usage: note_setup [patch ...]   (run from the repo root so patches
// resolve). Defaults to the same spread the Go benchmark uses.
#include <stdio.h>
#include <time.h>

#include "ondes/component.h"
#include "ondes/patch.h"
#include "ondes/synth.h"
#include "ondes/voice.h"

static double now_ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec * 1e9 + t.tv_nsec;
}

int main(int argc, char **argv) {
    component_register_all();
    const int N = 20000;
    const char *defaults[] = {"sine", "saw", "bassoon", "bell-organ", "brass", "ocean2"};
    const char **patches = (const char **)(argv + 1);
    int n = argc - 1;
    if (n == 0) {
        patches = defaults;
        n = (int)(sizeof(defaults) / sizeof(defaults[0]));
    }

    printf("%-14s %13s %12s %9s\n", "patch", "fresh ns/op", "pool ns/op",
           "speedup");
    for (int a = 0; a < n; a++) {
        char e[256];
        Patch *p = patch_load(patches[a], e, sizeof(e));
        if (!p) {
            printf("%-14s  load fail: %s\n", patches[a], e);
            continue;
        }
        Synth *s = synth_new(44100, patch_as_synth_patch(p));
        SynthPatch sp = patch_as_synth_patch(p);

        // fresh: cold voice_new + Apply (matches Go's NoteOnSetup).
        double build = 0;
        for (int i = 0; i < N; i++) {
            double t0 = now_ns();
            Voice *v = voice_new(s, 0, 60, 100);
            sp.apply(sp.ctx, v);
            build += now_ns() - t0;
            voice_release_clocks(v);
            voice_free(v);
        }

        // pool: build one template, then time the snapshot-restore reset.
        Voice *tv = voice_new(s, 0, 60, 100);
        sp.apply(sp.ctx, tv);
        voice_build_snapshot(tv);
        double reset = 0;
        for (int i = 0; i < N; i++) {
            double t0 = now_ns();
            voice_reset_for_reuse(tv, 0, 60, 100);
            reset += now_ns() - t0;
        }
        voice_release_clocks(tv);
        voice_free(tv);

        double bf = build / N, rs = reset / N;
        printf("%-14s %13.1f %12.1f %8.1fx\n", patches[a], bf, rs, bf / rs);
        synth_free(s);
        patch_free(p);
    }
    return 0;
}
