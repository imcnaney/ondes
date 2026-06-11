// `op-amp`: multiplies its inputs together, then scales the product.
// Used as a VCA and as a ring modulator. Ported from
// component/opamp/opamp.go.
#include <math.h>

#include "computil.h"
#include "ondes/component.h"

// engineIntScale is the ratio between Java's ampBase (1024) and our
// matching float amplitude (waveDefaultLevel = 0.025). Each multiplied
// input beyond the first contributes one factor.
static const double ENGINE_INT_SCALE = 1024.0 / 0.025;

typedef struct {
    Component base;
    Wire *out;
    WireList inputs;
    double scale;
    double corr; // engineIntScale^(numInputs-1), computed lazily
} OpAmp;

static double opamp_compute(void *ctx) {
    OpAmp *o = ctx;
    if (o->inputs.n == 0) return 0;
    if (o->corr == 0) o->corr = pow(ENGINE_INT_SCALE, (double)(o->inputs.n - 1));
    double prod = 1.0;
    for (size_t i = 0; i < o->inputs.n; i++) prod *= wire_get(o->inputs.w[i]);
    return prod * o->scale * o->corr;
}

static Wire *opamp_output(Component *self) { return ((OpAmp *)self)->out; }

static void opamp_add_input(Component *self, const char *sel, Wire *w) {
    (void)sel;
    wirelist_add(&((OpAmp *)self)->inputs, w);
}

static int opamp_configure(Component *self, const Spec *spec, Voice *v,
                           const char *name) {
    (void)name;
    OpAmp *o = (OpAmp *)self;
    wirelist_init(&o->inputs, voice_arena(v));
    o->scale = spec_double(spec, "level-scale", 1.0);
    o->out = voice_new_wire(v, opamp_compute, o);
    return 0;
}

static const ComponentVTable OPAMP_VT = {
    .configure = opamp_configure,
    .output = opamp_output,
    .add_input = opamp_add_input,
};

Component *opamp_new(Arena *a) {
    OpAmp *o = arena_alloc(a, sizeof(*o));
    o->base.vt = &OPAMP_VT;
    return &o->base;
}
