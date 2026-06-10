// `echo`: a feedback delay line. Ported from component/echo/echo.go.
//   y[n] = x[n] + amount * tape[t0]
//   tape[(t0+offset) % len] = y[n]
#include <math.h>
#include <string.h>

#include "computil.h"
#include "ondes/component.h"
#include "ondes/synth.h"

typedef struct {
    Component base;
    WireList inputs;
    WireList amount_mod;
    Wire *out;
    double *tape;
    int tape_len;
    int t0;
    int offset;
    double amount_base; // feedback fraction before modulation (config/100)
    double level_scale;
} Echo;

static double echo_compute(void *ctx) {
    Echo *e = ctx;
    double amount = e->amount_base;
    if (e->amount_mod.n > 0) {
        double delta = wirelist_sum(&e->amount_mod);
        amount += delta * 32767.0 / 100.0;
    }
    double x0 = wirelist_sum(&e->inputs);
    double y0 = x0 + e->tape[e->t0] * amount;
    e->tape[(e->t0 + e->offset) % e->tape_len] = y0;
    e->t0 = (e->t0 + 1) % e->tape_len;
    return e->level_scale * y0;
}

static Wire *echo_output(Component *self) { return ((Echo *)self)->out; }

static void echo_add_input(Component *self, const char *sel, Wire *w) {
    Echo *e = (Echo *)self;
    if (sel && !strcmp(sel, "amount"))
        wirelist_add(&e->amount_mod, w);
    else
        wirelist_add(&e->inputs, w);
}

static int echo_configure(Component *self, const Spec *spec, Voice *v,
                          const char *name) {
    (void)name;
    Echo *e = (Echo *)self;
    Arena *a = voice_arena(v);
    wirelist_init(&e->inputs, a);
    wirelist_init(&e->amount_mod, a);
    e->level_scale = spec_double(spec, "level-scale", 1.0);
    e->amount_base = spec_double(spec, "amount", 0.0) / 100.0;

    double time_ms = spec_double(spec, "time", 1000.0);
    double sr = (double)synth_sample_rate(voice_synth(v));
    int n = (int)ceil(time_ms / 1000.0 * sr);
    if (n < 1) n = 1;
    e->tape = arena_alloc(a, (size_t)n * sizeof(double));
    e->tape_len = n;
    e->offset = n;

    e->out = voice_new_wire(v, echo_compute, e);
    return 0;
}

static const ComponentVTable ECHO_VT = {
    .configure = echo_configure,
    .output = echo_output,
    .add_input = echo_add_input,
};

Component *echo_new(Arena *a) {
    Echo *e = arena_alloc(a, sizeof(*e));
    e->base.vt = &ECHO_VT;
    return &e->base;
}
