// A Wire is one node's output in the pull-driven audio graph (the C
// equivalent of Java's WiredIntSupplier / the Go port's synth.Wire).
// Each sample it can be read any number of times; only the first call
// computes, the rest return the latched value. That latch is what lets a
// component feed its own output back in (FM) without infinite recursion.
// wire_reset runs once per sample at the top of the synth main loop.
#ifndef ONDES_WIRE_H
#define ONDES_WIRE_H

#include <stdbool.h>
#include <stdint.h>

// ondes_wire_gen is the current sample "generation". A wire has been
// computed this sample iff its gen matches. Advancing it once per sample
// (wire_advance_gen) invalidates every wire in O(1) - no per-wire reset
// pass. Single-threaded: one synth steps at a time (the live engine is
// owned by the audio callback), so a shared counter is safe.
extern uint64_t ondes_wire_gen;

typedef struct Wire {
    double (*compute)(void *ctx); // pull function; ctx is the owning component
    void *ctx;
    double cached;
    uint64_t gen; // generation at which `cached` was computed
} Wire;

// wire_init sets up an already-allocated wire (Wires live in the voice
// arena; allocate them via voice_new_wire).
void wire_init(Wire *w, double (*compute)(void *ctx), void *ctx);

static inline double wire_get(Wire *w) {
    if (w->gen != ondes_wire_gen) {
        w->cached = w->compute(w->ctx);
        w->gen = ondes_wire_gen;
    }
    return w->cached;
}

// wire_advance_gen invalidates every wire for the next sample. Call once
// per sample at the top of the synth main loop.
static inline void wire_advance_gen(void) { ondes_wire_gen++; }

#endif // ONDES_WIRE_H
