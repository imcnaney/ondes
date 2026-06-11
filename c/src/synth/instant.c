#include "ondes/instant.h"

#include <stdlib.h>

void phase_clock_set_frequency(PhaseClock *p, double hz) {
    p->frequency = hz;
    p->delta = hz / (double)p->sr;
}

static void phase_clock_tick(PhaseClock *p) {
    p->phase += p->delta;
    while (p->phase >= 1.0) p->phase -= 1.0;
}

Instant *instant_new(int sr) {
    Instant *i = calloc(1, sizeof(*i));
    i->sr = sr;
    return i;
}

void instant_free(Instant *i) {
    if (!i) return;
    // Each PhaseClock is malloc'd individually (it outlives the arena-free
    // boundary only conceptually; voices remove their own clocks before
    // teardown). Any still registered at shutdown are freed here.
    for (size_t j = 0; j < i->n_clocks; j++) free(i->clocks[j]);
    free(i->clocks);
    free(i);
}

PhaseClock *instant_add_phase_clock(Instant *i) {
    if (i->n_clocks == i->cap_clocks) {
        size_t cap = i->cap_clocks ? i->cap_clocks * 2 : 16;
        i->clocks = realloc(i->clocks, cap * sizeof(*i->clocks));
        i->cap_clocks = cap;
    }
    PhaseClock *pc = calloc(1, sizeof(*pc));
    pc->sr = i->sr;
    i->clocks[i->n_clocks++] = pc;
    return pc;
}

void instant_remove_clock(Instant *i, PhaseClock *pc) {
    // Order is irrelevant - clocks are independent - so a swap-remove is
    // fine. Without this, every note ever played would keep ticking for
    // the life of the synth.
    for (size_t j = 0; j < i->n_clocks; j++) {
        if (i->clocks[j] == pc) {
            i->clocks[j] = i->clocks[i->n_clocks - 1];
            i->n_clocks--;
            free(pc);
            return;
        }
    }
}

void instant_next(Instant *i) {
    i->sample++;
    for (size_t j = 0; j < i->n_clocks; j++) phase_clock_tick(i->clocks[j]);
}
