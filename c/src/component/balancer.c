// `balancer`: a controller-driven crossfade between `left` and `right`
// inputs. Ported from component/balancer/balancer.go.
//   lScale = (ctrl/ctrlInputAmp + 1) / 2
//   out    = (lScale*left + (1-lScale)*right) * level-scale
#include <string.h>

#include "computil.h"
#include "ondes/component.h"

static const double INT_SCALE = 32767.0;

typedef struct {
    Component base;
    Wire *out;
    WireList left, right, ctrl;
    double scale;
    double ctrl_input_amp_inv; // intScale / ctrlInputAmp
} Balancer;

static double balancer_compute(void *ctx) {
    Balancer *b = ctx;
    double l = wirelist_sum(&b->left);
    double r = wirelist_sum(&b->right);
    double c = wirelist_sum(&b->ctrl);
    double l_scale = (c * b->ctrl_input_amp_inv + 1) / 2;
    return (l_scale * l + (1 - l_scale) * r) * b->scale;
}

static Wire *balancer_output(Component *self) { return ((Balancer *)self)->out; }

static void balancer_add_input(Component *self, const char *sel, Wire *w) {
    Balancer *b = (Balancer *)self;
    if (sel && !strcmp(sel, "left"))
        wirelist_add(&b->left, w);
    else if (sel && !strcmp(sel, "right"))
        wirelist_add(&b->right, w);
    else if (sel && !strcmp(sel, "ctrl"))
        wirelist_add(&b->ctrl, w);
}

static int balancer_configure(Component *self, const Spec *spec, Voice *v,
                              const char *name) {
    (void)name;
    Balancer *b = (Balancer *)self;
    Arena *a = voice_arena(v);
    wirelist_init(&b->left, a);
    wirelist_init(&b->right, a);
    wirelist_init(&b->ctrl, a);
    b->scale = spec_double(spec, "level-scale", 1.0);

    double ctrl_input_amp = 1000.0; // Java default
    // input-ctrl: { amp, initial-value } - both keys required to override.
    const Spec *ic = spec_get(spec, "input-ctrl");
    double amp, iv;
    if (spec_is_map(ic) && spec_num(ic, "amp", &amp) &&
        spec_num(ic, "initial-value", &iv))
        ctrl_input_amp = amp;
    b->ctrl_input_amp_inv = INT_SCALE / ctrl_input_amp;

    b->out = voice_new_wire(v, balancer_compute, b);
    return 0;
}

static const ComponentVTable BALANCER_VT = {
    .configure = balancer_configure,
    .output = balancer_output,
    .add_input = balancer_add_input,
};

Component *balancer_new(Arena *a) {
    Balancer *b = arena_alloc(a, sizeof(*b));
    b->base.vt = &BALANCER_VT;
    return &b->base;
}
