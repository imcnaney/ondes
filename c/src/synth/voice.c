#include "ondes/voice.h"

#include <math.h>
#include <stdlib.h>
#include <string.h>

#include "ondes/component.h"
#include "ondes/synth.h"

// --- Junction ---

static double junction_sum(void *ctx) {
    Junction *j = ctx;
    double s = 0;
    for (size_t i = 0; i < j->n_inputs; i++) s += wire_get(j->inputs[i]);
    return s;
}

Junction *junction_new(Voice *v) {
    Junction *j = arena_alloc(&v->arena, sizeof(*j));
    j->arena = &v->arena;
    j->out = voice_new_wire(v, junction_sum, j);
    return j;
}

void junction_add_input(Junction *j, Wire *w) {
    if (j->n_inputs == j->cap_inputs) {
        size_t cap = j->cap_inputs ? j->cap_inputs * 2 : 4;
        j->inputs = arena_grow(j->arena, j->inputs, j->n_inputs, cap,
                               sizeof(*j->inputs));
        j->cap_inputs = cap;
    }
    j->inputs[j->n_inputs++] = w;
}

Wire *junction_output(Junction *j) { return j->out; }

// --- Voice ---

Voice *voice_new(Synth *s, uint8_t ch, uint8_t note, uint8_t vel) {
    Voice *v = calloc(1, sizeof(*v));
    v->note = note;
    v->chan = ch;
    v->velocity = vel;
    v->synth = s;
    arena_init(&v->arena);
    v->voice_mix = junction_new(v);
    return v;
}

void voice_build_snapshot(Voice *v) {
    arena_snapshot_free(v->snapshot);
    v->snapshot = arena_snapshot(&v->arena);
}

void voice_reset_for_reuse(Voice *v, uint8_t ch, uint8_t note, uint8_t vel) {
    arena_restore(&v->arena, v->snapshot);
    for (size_t i = 0; i < v->n_comps; i++) component_reset(v->comps[i].c);
    v->note = note;
    v->chan = ch;
    v->velocity = vel;
    v->draining = false;
    v->zero_count = 0;
    // wait_for_env is a property of the patch (set in Apply) and is
    // unchanged across reuses, so it is intentionally left as is.
}

void voice_free(Voice *v) {
    if (!v) return;
    arena_snapshot_free(v->snapshot);
    v->snapshot = NULL;
    // Defensively release any phase clocks still registered on the shared
    // Instant before dropping the voice's arena. Normal teardown
    // (remove_voice) already releases them - this is idempotent - but it
    // also covers the patch-apply-failure path, where a partially built
    // voice may have allocated oscillator clocks before a later component
    // failed. Without this they would leak and keep ticking.
    voice_release_clocks(v);
    arena_free(&v->arena);
    free(v);
}

Synth *voice_synth(Voice *v) { return v->synth; }
Arena *voice_arena(Voice *v) { return &v->arena; }

double voice_note_freq(const Voice *v) {
    return 440.0 * pow(2.0, ((double)v->note - 69.0) / 12.0);
}

Wire *voice_new_wire(Voice *v, double (*compute)(void *), void *ctx) {
    Wire *w = arena_alloc(&v->arena, sizeof(*w));
    wire_init(w, compute, ctx);
    return w;
}

PhaseClock *voice_add_phase_clock(Voice *v) {
    PhaseClock *pc = instant_add_phase_clock(synth_instant(v->synth));
    if (v->n_clocks == v->cap_clocks) {
        size_t cap = v->cap_clocks ? v->cap_clocks * 2 : 4;
        v->clocks = arena_grow(&v->arena, v->clocks, v->n_clocks, cap,
                               sizeof(*v->clocks));
        v->cap_clocks = cap;
    }
    v->clocks[v->n_clocks++] = pc;
    return pc;
}

void voice_release_clocks(Voice *v) {
    Instant *inst = synth_instant(v->synth);
    for (size_t i = 0; i < v->n_clocks; i++)
        instant_remove_clock(inst, v->clocks[i]);
    v->n_clocks = 0;
}

void voice_add_voice_mix_input(Voice *v, Wire *w) {
    junction_add_input(v->voice_mix, w);
}

Wire *voice_main_output(Voice *v) { return junction_output(v->voice_mix); }

void voice_add_component(Voice *v, const char *name, Component *c) {
    if (v->n_comps == v->cap_comps) {
        size_t cap = v->cap_comps ? v->cap_comps * 2 : 8;
        v->comps =
            arena_grow(&v->arena, v->comps, v->n_comps, cap, sizeof(*v->comps));
        v->cap_comps = cap;
    }
    v->comps[v->n_comps].name = arena_strdup(&v->arena, name);
    v->comps[v->n_comps].c = c;
    v->n_comps++;
}

Component *voice_component(Voice *v, const char *name) {
    for (size_t i = 0; i < v->n_comps; i++)
        if (strcmp(v->comps[i].name, name) == 0) return v->comps[i].c;
    return NULL;
}

void voice_dispatch_midi(Voice *v, MidiMsg m) {
    for (size_t i = 0; i < v->n_comps; i++) component_on_midi(v->comps[i].c, m);
}

void voice_set_wait_for_env(Voice *v, bool b) { v->wait_for_env = b; }
bool voice_wait_for_env(const Voice *v) { return v->wait_for_env; }
void voice_start_draining(Voice *v) { v->draining = true; }
