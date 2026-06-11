// `env`: the multi-segment envelope. A list of (rate, level [, option])
// steps the level walks through, with markers for re-trigger, hold,
// release and alt-release. Attenuates the sum of its inputs by the
// per-sample level, and optionally signals voice termination on exit.
// Ported from component/env/env.go.
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "computil.h"
#include "ondes/component.h"
#include "ondes/synth.h"

typedef enum { STEP_LINEAR, STEP_HOLD } StepKind;

typedef struct {
    StepKind kind;
    double rate, level; // rate in ms, level 0..100
    int sample_rate;
    double k, m;
    int64_t hold_start_sample;
} Step;

typedef struct {
    Component base;
    Voice *voice;
    Wire *out;
    WireList inputs;

    Wire *level_out; // sidechain (out-level:)
    double out_level_min, out_level_max;
    int64_t last_advance_sample;

    Step *steps;
    size_t n_steps;
    int cur_step;
    int re_trigger, hold, release, alt_release;

    double cur_level;
    bool note_on, first_note, pre_release, exit_, ended;
    double level_scale;
    uint8_t chan_no, note_no;
    int sample_rate;
} Env;

static double clip100(double v) {
    if (v < 0) return 0;
    if (v > 100) return 100;
    return v;
}

static Step make_step(double rate, double level, int sr) {
    Step s = {0};
    s.kind = STEP_LINEAR;
    s.rate = rate < 0 ? 0 : rate;
    s.level = clip100(level);
    s.sample_rate = sr;
    if (rate > 0) {
        double d = rate * (double)sr / 1000.0 / 4.616;
        s.k = 1.0 / d;
        s.m = 1.0 / d;
    }
    return s;
}

// --- step value advance ---

static void advance_linear(Step *s, double cur, double *out, bool *done) {
    if (s->rate == 0 || cur == s->level) {
        *out = s->level;
        *done = true;
        return;
    }
    double delta = s->level - cur;
    double sign = delta < 0 ? -1.0 : 1.0;
    double next = cur + sign * s->k + delta * s->m;
    if ((cur > s->level && next <= s->level) ||
        (cur < s->level && next >= s->level)) {
        *out = s->level;
        *done = true;
        return;
    }
    *out = clip100(next);
    *done = false;
}

static void next_val(Step *s, double cur, int64_t cur_sample, double *out,
                     bool *done) {
    if (s->kind == STEP_HOLD) {
        if (cur != s->level) {
            advance_linear(s, cur, out, done);
            return;
        }
        int64_t hold_samples =
            (int64_t)(s->rate * (double)s->sample_rate / 1000.0);
        *out = s->level;
        *done = cur_sample - s->hold_start_sample >= hold_samples;
        return;
    }
    advance_linear(s, cur, out, done);
}

// --- env state machine ---

static int64_t env_sample(Env *e) {
    return instant_sample(synth_instant(voice_synth(e->voice)));
}

static void set_cur_step(Env *e, int idx) {
    if (idx < 0 || idx >= (int)e->n_steps) return;
    e->cur_step = idx;
    if (idx == e->release) e->pre_release = false;
    if (e->steps[idx].kind == STEP_HOLD)
        e->steps[idx].hold_start_sample = env_sample(e);
}

static void env_advance(Env *e) {
    if (e->cur_step == e->hold && e->note_on) return;
    int last = (int)e->n_steps - 1;
    int end_idx = last;
    if (e->alt_release > 0) end_idx = e->alt_release - 1;
    if (e->cur_step == end_idx) {
        if (e->exit_ && e->cur_level == 0) {
            synth_queue_note_end(voice_synth(e->voice), e->chan_no, e->note_no);
            e->ended = true;
        }
        return;
    }
    set_cur_step(e, e->cur_step + 1);
}

static void advance_once(Env *e) {
    int64_t cur = env_sample(e);
    if (cur == e->last_advance_sample || e->ended) return;
    e->last_advance_sample = cur;
    Step *s = &e->steps[e->cur_step];
    double level;
    bool done;
    next_val(s, e->cur_level, cur, &level, &done);
    e->cur_level = level;
    if (done) env_advance(e);
}

static double env_compute(void *ctx) {
    Env *e = ctx;
    advance_once(e);
    if (e->ended) return 0;
    double sum = wirelist_sum(&e->inputs);
    return sum * (e->cur_level / 100.0) * e->level_scale;
}

static double env_compute_level(void *ctx) {
    Env *e = ctx;
    advance_once(e);
    return (e->out_level_min +
            (e->out_level_max - e->out_level_min) * e->cur_level / 100) /
           32767.0;
}

static Wire *env_output(Component *self) { return ((Env *)self)->out; }

static Wire *env_named_output(Component *self, const char *key) {
    Env *e = (Env *)self;
    if (!strcmp(key, "out-level")) return e->level_out;
    return NULL;
}

static void env_add_input(Component *self, const char *sel, Wire *w) {
    (void)sel;
    wirelist_add(&((Env *)self)->inputs, w);
}

static void env_on_midi(Component *self, MidiMsg m) {
    Env *e = (Env *)self;
    if (midi_is_note_on(m)) {
        e->note_on = true;
        e->chan_no = m.status & 0x0F;
        e->note_no = m.data1;
        if (e->first_note)
            e->cur_step = 0;
        else if (e->re_trigger >= 0)
            e->cur_step = e->re_trigger;
        else
            e->cur_step = 0;
        e->first_note = false;
        e->ended = false;
        e->pre_release = true;
    } else if (midi_is_note_off(m)) {
        e->note_on = false;
        e->pre_release = false;
        if (e->cur_step == e->hold)
            set_cur_step(e, e->hold + 1);
        else if (e->release >= 0)
            set_cur_step(e, e->release);
    }
}

// --- a small malloc-backed step vector used during parse (supports the
// alt-release splice); copied into the voice arena at the end. ---

typedef struct {
    Step *a;
    size_t n, cap;
} StepVec;

static void sv_push(StepVec *v, Step s) {
    if (v->n == v->cap) {
        v->cap = v->cap ? v->cap * 2 : 8;
        v->a = realloc(v->a, v->cap * sizeof(Step));
    }
    v->a[v->n++] = s;
}

static void sv_insert(StepVec *v, size_t idx, Step s) {
    sv_push(v, s); // grow by one
    memmove(&v->a[idx + 1], &v->a[idx], (v->n - 1 - idx) * sizeof(Step));
    v->a[idx] = s;
}

// strip_dashes copies tok without '-' into buf.
static void strip_dashes(const char *tok, char *buf, size_t buflen) {
    size_t j = 0;
    for (size_t i = 0; tok[i] && j + 1 < buflen; i++)
        if (tok[i] != '-') buf[j++] = tok[i];
    buf[j] = 0;
}

static int env_parse_points(Env *e, const Spec *pts) {
    StepVec sv = {0};
    int re_trigger = -1, hold = -1, release = -1, alt_release = -1;
    int rc = 0;

    for (size_t pi = 0; pi < pts->n_items; pi++) {
        const Spec *raw = pts->items[pi];
        if (!raw) continue;
        // Build the line string: scalar as-is, or join a flow seq by spaces.
        char line[256];
        line[0] = 0;
        if (raw->kind == SPEC_SCALAR) {
            snprintf(line, sizeof(line), "%s", raw->scalar ? raw->scalar : "");
        } else if (raw->kind == SPEC_SEQ) {
            size_t off = 0;
            for (size_t j = 0; j < raw->n_items; j++) {
                const char *t = (raw->items[j] && raw->items[j]->kind == SPEC_SCALAR)
                                    ? raw->items[j]->scalar
                                    : "";
                int w = snprintf(line + off, sizeof(line) - off, "%s%s",
                                 j ? " " : "", t);
                if (w < 0 || (size_t)w >= sizeof(line) - off) break;
                off += (size_t)w;
            }
        } else {
            continue;
        }

        // Tokenize by space/tab/comma.
        char *tokens[16];
        int nt = 0;
        for (char *p = strtok(line, " \t,"); p && nt < 16;
             p = strtok(NULL, " \t,"))
            tokens[nt++] = p;
        if (nt < 2) {
            rc = -1;
            goto done;
        }
        char *end;
        double rate = strtod(tokens[0], &end);
        if (end == tokens[0]) { rc = -1; goto done; }
        double level = strtod(tokens[1], &end);
        if (end == tokens[1]) { rc = -1; goto done; }

        // Options (dashes stripped), and alt-release detection.
        char opts[14][32];
        int nopts = 0;
        bool is_alt_release = false;
        for (int t = 2; t < nt && nopts < 14; t++) {
            strip_dashes(tokens[t], opts[nopts], sizeof(opts[nopts]));
            if (!strcmp(opts[nopts], "altrelease")) is_alt_release = true;
            nopts++;
        }

        // 0,0 lead-in if the first step doesn't start there.
        if (sv.n == 0 && rate != 0)
            sv_push(&sv, make_step(0, 0, e->sample_rate));

        // Repeated level becomes a Hold, unless this is the alt-release.
        if (sv.n > 0 && !is_alt_release && sv.a[sv.n - 1].level == level) {
            Step s = make_step(rate, level, e->sample_rate);
            s.kind = STEP_HOLD;
            sv_push(&sv, s);
        } else {
            sv_push(&sv, make_step(rate, level, e->sample_rate));
        }
        for (int o = 0; o < nopts; o++) {
            if (!strcmp(opts[o], "retrigger"))
                re_trigger = (int)sv.n - 1;
            else if (!strcmp(opts[o], "hold"))
                hold = (int)sv.n - 1;
            else if (!strcmp(opts[o], "release"))
                release = (int)sv.n - 1;
            else if (!strcmp(opts[o], "altrelease"))
                alt_release = (int)sv.n - 1;
        }
    }

    if (sv.n == 0 || sv.a[sv.n - 1].level != 0)
        sv_push(&sv, make_step(0, 0, e->sample_rate));
    if (hold >= 0 && sv.a[hold].level == 0) hold = -1;
    if (alt_release > 0 && sv.a[alt_release - 1].level != 0) {
        sv_insert(&sv, alt_release, make_step(0, 0, e->sample_rate));
        alt_release++;
    }
    if (alt_release > 0) {
        release = alt_release - 1;
    } else if (release < 0) {
        if (hold >= 0) {
            release = hold + 1;
            if (release > (int)sv.n - 1) release = (int)sv.n - 1;
        } else {
            release = (int)sv.n - 1;
        }
    }

    // Copy into the voice arena.
    e->steps = arena_alloc(voice_arena(e->voice), sv.n * sizeof(Step));
    memcpy(e->steps, sv.a, sv.n * sizeof(Step));
    e->n_steps = sv.n;
    e->re_trigger = re_trigger;
    e->hold = hold;
    e->release = release;
    e->alt_release = alt_release;

done:
    free(sv.a);
    return rc;
}

static int env_configure(Component *self, const Spec *spec, Voice *v,
                         const char *name) {
    (void)name;
    Env *e = (Env *)self;
    e->voice = v;
    wirelist_init(&e->inputs, voice_arena(v));
    e->sample_rate = synth_sample_rate(voice_synth(v));
    e->first_note = true;
    e->pre_release = true;
    e->level_scale = 1;
    e->last_advance_sample = -1;
    e->re_trigger = e->hold = e->release = e->alt_release = -1;

    if (spec_get_str(spec, "out-level")) {
        double amp;
        if (spec_num(spec, "out-level-amp", &amp)) {
            e->out_level_min = 0;
            e->out_level_max = amp;
        }
        e->level_out = voice_new_wire(v, env_compute_level, e);
    }

    if (spec_bool(spec, "exit", false)) {
        e->exit_ = true;
        voice_set_wait_for_env(v, true);
    }
    double ls;
    if (spec_num(spec, "level-scale", &ls)) e->level_scale = ls;

    const Spec *pts = spec_get(spec, "points");
    if (!spec_is_seq(pts)) return -1;
    if (env_parse_points(e, pts) != 0) return -1;

    e->out = voice_new_wire(v, env_compute, e);
    return 0;
}

static const ComponentVTable ENV_VT = {
    .configure = env_configure,
    .output = env_output,
    .add_input = env_add_input,
    .on_midi = env_on_midi,
    .named_output = env_named_output,
};

Component *env_new(Arena *a) {
    Env *e = arena_alloc(a, sizeof(*e));
    e->base.vt = &ENV_VT;
    return &e->base;
}
