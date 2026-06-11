// render_throughput measures the per-sample cost of the engine hot path
// (synth_step) under a held chord - the number that decides how much
// polyphony fits in real time. Usage:
//   render_throughput <patch> [voices]   (run from the repo root)
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

#include "ondes/component.h"
#include "ondes/patch.h"
#include "ondes/synth.h"

static double now_ns(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec * 1e9 + t.tv_nsec;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: %s <patch> [voices]\n", argv[0]);
        return 2;
    }
    component_register_all();
    int voices = argc > 2 ? atoi(argv[2]) : 8;
    const int SR = 44100, SECS = 20;

    char e[256];
    Patch *p = patch_load(argv[1], e, sizeof(e));
    if (!p) {
        fprintf(stderr, "load %s: %s\n", argv[1], e);
        return 1;
    }
    Synth *s = synth_new(SR, patch_as_synth_patch(p));
    for (int i = 0; i < voices; i++) synth_note_on(s, 0, 48 + i * 2, 100);

    for (int i = 0; i < SR / 10; i++) synth_step(s); // warm up
    long total = (long)SR * SECS;
    double t0 = now_ns();
    volatile double acc = 0;
    for (long i = 0; i < total; i++) acc += synth_step(s);
    double per = (now_ns() - t0) / total;

    int live = synth_active_voices(s);
    printf("%-12s voices=%-2d  %.1f ns/sample  %.2f ns/voice  realtime=%.0fx  "
           "(%.1f%% of one core)\n",
           argv[1], live, per, per / (live ? live : 1), (1e9 / SR) / per,
           per * SR / 1e7);
    synth_free(s);
    patch_free(p);
    return 0;
}
