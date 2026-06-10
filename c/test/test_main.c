// Unit tests for the engine core: arena, wire latch/reset, instant clock
// lifecycle, and voice keying/teardown. Run via CTest (ondes_tests).
#include <math.h>
#include <stdio.h>

#include "ondes/arena.h"
#include "ondes/component.h"
#include "ondes/synth.h"

static int failures;
#define CHECK(cond, msg)                                                        \
    do {                                                                        \
        if (!(cond)) {                                                          \
            fprintf(stderr, "FAIL: %s (%s:%d)\n", msg, __FILE__, __LINE__);     \
            failures++;                                                         \
        }                                                                       \
    } while (0)

// --- arena ---
static void test_arena(void) {
    Arena a;
    arena_init(&a);
    int *p = arena_alloc(&a, 10 * sizeof(int));
    CHECK(p != NULL, "arena_alloc");
    for (int i = 0; i < 10; i++) CHECK(p[i] == 0, "arena zeroed");
    int *q = arena_alloc(&a, 100000); // forces a new block
    CHECK(q != NULL, "arena big alloc");
    int *grown = arena_grow(&a, p, 10, 20, sizeof(int));
    CHECK(grown != NULL, "arena_grow");
    arena_free(&a);
}

// --- wire latch / reset ---
static int compute_calls;
static double counting_compute(void *ctx) {
    (void)ctx;
    compute_calls++;
    return 1.0;
}

static void test_wire(void) {
    Wire w;
    wire_init(&w, counting_compute, NULL);
    compute_calls = 0;
    CHECK(wire_get(&w) == 1.0, "wire value");
    wire_get(&w);
    wire_get(&w);
    CHECK(compute_calls == 1, "wire computes once per sample");
    wire_reset(&w);
    wire_get(&w);
    CHECK(compute_calls == 2, "wire recomputes after reset");
}

// --- instant clocks ---
static void test_instant(void) {
    Instant *in = instant_new(44100);
    CHECK(instant_active_clocks(in) == 0, "instant starts empty");
    PhaseClock *a = instant_add_phase_clock(in);
    PhaseClock *b = instant_add_phase_clock(in);
    CHECK(instant_active_clocks(in) == 2, "two clocks registered");
    phase_clock_set_frequency(a, 44100.0 / 4); // wraps every 4 samples
    instant_next(in);
    CHECK(fabs(phase_clock_phase(a) - 0.25) < 1e-9, "clock ticks");
    instant_remove_clock(in, a);
    CHECK(instant_active_clocks(in) == 1, "clock removed");
    instant_remove_clock(in, b);
    CHECK(instant_active_clocks(in) == 0, "all clocks removed");
    instant_free(in);
}

// --- voice keying + clock release via an inline patch ---
static int test_apply(void *ctx, Voice *v) {
    (void)ctx;
    PhaseClock *pc = voice_add_phase_clock(v);
    phase_clock_set_frequency(pc, 440.0);
    // a trivial constant wire into the main mix so the voice produces output
    Wire *w = voice_new_wire(v, counting_compute, NULL);
    voice_add_voice_mix_input(v, w);
    return 0;
}

static void test_voice_lifecycle(void) {
    static int dummy; // ctx must be non-NULL (NULL means "no patch")
    SynthPatch sp = {.ctx = &dummy, .apply = test_apply};
    Synth *s = synth_new(44100, sp);

    synth_note_on(s, 0, 60, 100);
    synth_note_on(s, 1, 60, 100); // same note, different channel = 2 voices
    CHECK(synth_active_voices(s) == 2, "two voices keyed by (ch,note)");
    CHECK(instant_active_clocks(synth_instant(s)) == 2, "two clocks live");

    synth_step(s);

    synth_note_off(s, 0, 60);
    CHECK(synth_active_voices(s) == 1, "voice removed on note-off");
    CHECK(instant_active_clocks(synth_instant(s)) == 1,
          "clock released with voice");

    synth_note_off(s, 1, 60);
    CHECK(synth_active_voices(s) == 0, "all voices gone");
    CHECK(instant_active_clocks(synth_instant(s)) == 0, "no clocks leaked");

    synth_free(s);
}

int main(void) {
    test_arena();
    test_wire();
    test_instant();
    test_voice_lifecycle();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    printf("all tests passed\n");
    return 0;
}
