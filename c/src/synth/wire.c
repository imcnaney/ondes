#include "ondes/wire.h"

uint64_t ondes_wire_gen = 0;

void wire_init(Wire *w, double (*compute)(void *ctx), void *ctx) {
    w->compute = compute;
    w->ctx = ctx;
    w->cached = 0.0;
    // Stamp one generation behind the current one so the first read after
    // construction always computes (a fresh wire has no cached value).
    w->gen = ondes_wire_gen - 1;
}
