#include "ondes/wire.h"

void wire_init(Wire *w, double (*compute)(void *ctx), void *ctx) {
    w->compute = compute;
    w->ctx = ctx;
    w->cached = 0.0;
    w->visited = false;
}
