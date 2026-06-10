// `controller`: a MIDI CC listener that outputs the (scaled) current
// value of one numbered controller. Ported from
// component/controller/controller.go.
//   level = min + (value/127) * (max - min)
#include <string.h>

#include "computil.h"
#include "ondes/component.h"

typedef struct {
    Component base;
    uint8_t number;
    double min_level, max_level;
    double level;
    Wire *out;
} Controller;

static double controller_compute(void *ctx) {
    Controller *c = ctx;
    return c->level / 32767.0;
}

static Wire *controller_output(Component *self) {
    return ((Controller *)self)->out;
}

static void controller_on_midi(Component *self, MidiMsg m) {
    Controller *c = (Controller *)self;
    if ((m.status & 0xF0) != 0xB0 || m.data1 != c->number) return;
    c->level = c->min_level + (double)m.data2 / 127.0 * (c->max_level - c->min_level);
}

// parse_min_max mirrors getMinMaxLevel: a single number -> (0, n); a
// "min max" string -> both.
static bool parse_min_max(const Spec *spec, double *min, double *max) {
    const Spec *amp = spec_get(spec, "amp");
    if (!amp || amp->kind != SPEC_SCALAR) return false;
    double nums[2];
    size_t n = 0;
    bool ok = true;
    char *p = (char *)amp->scalar;
    while (*p && n < 2) {
        while (*p == ' ' || *p == ',' || *p == '\t') p++;
        if (!*p) break;
        char *end;
        double v = strtod(p, &end);
        if (end == p) { ok = false; break; }
        nums[n++] = v;
        p = end;
    }
    if (!ok || n == 0) return false;
    if (n == 1) {
        *min = 0;
        *max = nums[0];
    } else {
        *min = nums[0];
        *max = nums[1];
    }
    return true;
}

static int controller_configure(Component *self, const Spec *spec, Voice *v,
                                const char *name) {
    (void)name;
    Controller *c = (Controller *)self;
    double num;
    if (!spec_num(spec, "number", &num)) return -1;
    c->number = (uint8_t)num;
    if (!parse_min_max(spec, &c->min_level, &c->max_level)) return -1;
    c->out = voice_new_wire(v, controller_compute, c);
    return 0;
}

static const ComponentVTable CONTROLLER_VT = {
    .configure = controller_configure,
    .output = controller_output,
    .on_midi = controller_on_midi,
};

Component *controller_new(Arena *a) {
    Controller *c = arena_alloc(a, sizeof(*c));
    c->base.vt = &CONTROLLER_VT;
    return &c->base;
}
