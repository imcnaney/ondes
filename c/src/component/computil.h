// Internal helpers shared by component implementations: a small
// arena-backed wire list (most components sum a set of input wires) and a
// tokenizer for the space/comma-separated numeric lists used by the
// wave/env config (e.g. "1 1" harmonic pairs, "rate level opt" points).
#ifndef ONDES_COMPUTIL_H
#define ONDES_COMPUTIL_H

#include <stdlib.h>

#include "ondes/arena.h"
#include "ondes/spec.h"
#include "ondes/wire.h"

// spec_num reads an optional numeric scalar from a map, reporting presence.
// This is the C analogue of the Go ports' `if v, ok := numeric(spec[k])`.
static inline bool spec_num(const Spec *m, const char *k, double *out) {
    const char *s = spec_get_str(m, k);
    if (!s || !*s) return false;
    char *e;
    double v = strtod(s, &e);
    if (e == s) return false;
    if (out) *out = v;
    return true;
}

typedef struct {
    Arena *a;
    Wire **w;
    size_t n, cap;
} WireList;

static inline void wirelist_init(WireList *l, Arena *a) {
    l->a = a;
    l->w = NULL;
    l->n = l->cap = 0;
}

static inline void wirelist_add(WireList *l, Wire *w) {
    if (l->n == l->cap) {
        size_t cap = l->cap ? l->cap * 2 : 4;
        l->w = arena_grow(l->a, l->w, l->n, cap, sizeof(*l->w));
        l->cap = cap;
    }
    l->w[l->n++] = w;
}

static inline double wirelist_sum(const WireList *l) {
    double s = 0;
    for (size_t i = 0; i < l->n; i++) s += wire_get(l->w[i]);
    return s;
}

// spec_tokens_doubles flattens a SPEC_SEQ (or single scalar) of
// space/comma/tab-separated numeric tokens into an arena array of doubles.
// Returns NULL with *n=0 on empty/parse error. *ok (if non-NULL) reports
// whether every token parsed cleanly.
double *spec_tokens_doubles(Arena *a, const Spec *s, size_t *n, bool *ok);

#endif // ONDES_COMPUTIL_H
