// `mix` and `dynamic-mix`: sum every input wire and multiply by a single
// level scale. `dynamic-mix` additionally tracks MIDI CC 7 (channel
// volume). Ported from component/mix/mix.go.
#include "computil.h"
#include "ondes/component.h"

typedef struct {
    Component base;
    Wire *out;
    WireList inputs;
    double level_scale;
    bool dynamic;
} Mix;

static double mix_compute(void *ctx) {
    Mix *m = ctx;
    return wirelist_sum(&m->inputs) * m->level_scale;
}

static Wire *mix_output(Component *self) { return ((Mix *)self)->out; }

static void mix_add_input(Component *self, const char *sel, Wire *w) {
    (void)sel;
    wirelist_add(&((Mix *)self)->inputs, w);
}

static void mix_on_midi(Component *self, MidiMsg m) {
    Mix *mix = (Mix *)self;
    if (!mix->dynamic) return;
    if ((m.status & 0xF0) == 0xB0 && m.data1 == 7)
        mix->level_scale = (double)m.data2 / 128.0;
}

static int mix_configure(Component *self, const Spec *spec, Voice *v,
                         const char *name) {
    (void)name;
    Mix *m = (Mix *)self;
    wirelist_init(&m->inputs, voice_arena(v));
    m->level_scale = spec_double(spec, "level-scale", 1.0);
    m->out = voice_new_wire(v, mix_compute, m);
    return 0;
}

static const ComponentVTable MIX_VT = {
    .configure = mix_configure,
    .output = mix_output,
    .add_input = mix_add_input,
    .on_midi = mix_on_midi,
};

Component *mix_new(Arena *a) {
    Mix *m = arena_alloc(a, sizeof(*m));
    m->base.vt = &MIX_VT;
    return &m->base;
}

Component *dynamic_mix_new(Arena *a) {
    Mix *m = arena_alloc(a, sizeof(*m));
    m->base.vt = &MIX_VT;
    m->dynamic = true;
    return &m->base;
}
