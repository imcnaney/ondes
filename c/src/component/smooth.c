// `smooth`: a non-linear smoother that fattens the bass of its input.
// Ported from component/smooth/smooth.go.
//   y0 += signum(delta)*k/intScale + delta*k
#include <math.h>
#include <stdio.h>
#include <string.h>

#include "computil.h"
#include "ondes/component.h"

static const double INT_SCALE = 32767.0;

typedef struct {
    Component base;
    Wire *out;
    WireList inputs;
    WireList range_in;

    double y0;
    double level_scale;
    double comp_scale;
    double k, k_inv;

    double amount;
    double amt_input_amp;
    double amt_input_range;
    bool mod_amt;
} Smooth;

static void smooth_set_k(Smooth *s, double inp) {
    s->k_inv = s->amount + inp * inp;
    s->k = 1.0 / s->k_inv;
    s->comp_scale = 1 + s->k_inv / 2.5;
}

static double smooth_compute(void *ctx) {
    Smooth *s = ctx;
    if (s->mod_amt) {
        double range_in = wirelist_sum(&s->range_in) * INT_SCALE;
        smooth_set_k(s, s->amt_input_range * (range_in / s->amt_input_amp));
    }
    double inp = wirelist_sum(&s->inputs);
    double delta = inp - s->y0;
    double sgn = 0;
    if (delta > 0)
        sgn = 1;
    else if (delta < 0)
        sgn = -1;
    s->y0 += sgn * s->k / INT_SCALE + delta * s->k;
    if ((s->y0 < inp && delta < 0) || (s->y0 > inp && delta > 0))
        s->y0 = inp; // don't overshoot
    return s->level_scale * s->y0 * s->comp_scale;
}

static Wire *smooth_output(Component *self) { return ((Smooth *)self)->out; }

static void smooth_add_input(Component *self, const char *sel, Wire *w) {
    Smooth *s = (Smooth *)self;
    if (!sel || !*sel || !strcmp(sel, "main"))
        wirelist_add(&s->inputs, w);
    else if (!strcmp(sel, "range"))
        wirelist_add(&s->range_in, w);
}

static int smooth_configure(Component *self, const Spec *spec, Voice *v,
                            const char *name) {
    (void)name;
    Smooth *s = (Smooth *)self;
    Arena *a = voice_arena(v);
    wirelist_init(&s->inputs, a);
    wirelist_init(&s->range_in, a);
    s->level_scale = 1;
    s->amount = 1;

    double amt;
    if (spec_num(spec, "amount", &amt)) s->amount = fabs(amt);
    if (s->amount < 1) {
        fprintf(stderr, "Smooth amount cannot be <1. Setting to 1\n");
        s->amount = 1;
    }
    smooth_set_k(s, 0);

    double ls;
    if (spec_num(spec, "level-scale", &ls)) {
        if (ls < 0 || ls > 11)
            fprintf(stderr, "smooth: 'level-scale' must be between 0 and 11\n");
        else
            s->level_scale = ls;
    }

    // input-amount: { amp, range } enables modulation of the amount.
    const Spec *ia = spec_get(spec, "input-amount");
    double amp, rng;
    if (spec_is_map(ia) && spec_num(ia, "amp", &amp) &&
        spec_num(ia, "range", &rng)) {
        s->amt_input_amp = amp;
        s->amt_input_range = rng;
        s->mod_amt = true;
    }

    s->out = voice_new_wire(v, smooth_compute, s);
    return 0;
}

static const ComponentVTable SMOOTH_VT = {
    .configure = smooth_configure,
    .output = smooth_output,
    .add_input = smooth_add_input,
};

Component *smooth_new(Arena *a) {
    Smooth *s = arena_alloc(a, sizeof(*s));
    s->base.vt = &SMOOTH_VT;
    return &s->base;
}
