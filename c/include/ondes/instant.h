// Instant tracks the current sample number and drives every PhaseClock.
// Oscillators get their phase from a PhaseClock allocated through the
// Voice (voice_add_phase_clock) so the voice can release it on teardown,
// keeping the tick list bounded to live voices.
#ifndef ONDES_INSTANT_H
#define ONDES_INSTANT_H

#include <stddef.h>
#include <stdint.h>

typedef struct PhaseClock {
    double frequency;
    double delta;
    double phase;
    int sr;
} PhaseClock;

void phase_clock_set_frequency(PhaseClock *p, double hz);
static inline double phase_clock_phase(const PhaseClock *p) { return p->phase; }
static inline void phase_clock_reset_phase(PhaseClock *p) { p->phase = 0; }

typedef struct Instant {
    int sr;
    int64_t sample;
    PhaseClock **clocks; // registered clocks, ticked every Next()
    size_t n_clocks;
    size_t cap_clocks;
} Instant;

Instant *instant_new(int sr);
void instant_free(Instant *i);

static inline int instant_sample_rate(const Instant *i) { return i->sr; }
static inline int64_t instant_sample(const Instant *i) { return i->sample; }
static inline double instant_seconds(const Instant *i) {
    return (double)i->sample / (double)i->sr;
}
// instant_active_clocks reports how many clocks are registered (for tests
// asserting clocks don't leak across notes).
static inline size_t instant_active_clocks(const Instant *i) {
    return i->n_clocks;
}

// instant_add_phase_clock registers and returns a fresh clock. Callers
// should go through voice_add_phase_clock so the voice tracks it.
PhaseClock *instant_add_phase_clock(Instant *i);

// instant_remove_clock unregisters a clock so Next no longer ticks it.
void instant_remove_clock(Instant *i, PhaseClock *pc);

// instant_next advances one sample and ticks every registered clock.
void instant_next(Instant *i);

#endif // ONDES_INSTANT_H
