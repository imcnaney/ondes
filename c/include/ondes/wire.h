// A Wire is one node's output in the pull-driven audio graph (the C
// equivalent of Java's WiredIntSupplier / the Go port's synth.Wire).
// Each sample it can be read any number of times; only the first call
// computes, the rest return the latched value. That latch is what lets a
// component feed its own output back in (FM) without infinite recursion.
// wire_reset runs once per sample at the top of the synth main loop.
#ifndef ONDES_WIRE_H
#define ONDES_WIRE_H

#include <stdbool.h>

typedef struct Wire {
    double (*compute)(void *ctx); // pull function; ctx is the owning component
    void *ctx;
    double cached;
    bool visited;
} Wire;

// wire_init sets up an already-allocated wire (Wires live in the voice
// arena; allocate them via voice_new_wire).
void wire_init(Wire *w, double (*compute)(void *ctx), void *ctx);

static inline double wire_get(Wire *w) {
    if (!w->visited) {
        w->cached = w->compute(w->ctx);
        w->visited = true;
    }
    return w->cached;
}

static inline void wire_reset(Wire *w) { w->visited = false; }

#endif // ONDES_WIRE_H
