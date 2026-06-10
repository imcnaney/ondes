// `filter`: IIR (coefficient sets from the library), sinc (running-average
// box low-pass) and biquad (RBJ-style low-pass with Java's custom alpha).
// Ported from component/filter/filter.go. Per-voice only (channel-context
// shared filters are not supported, matching the Go port).
#include <math.h>
#include <string.h>

#include "computil.h"
#include "iir.h"
#include "ondes/component.h"
#include "ondes/synth.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

typedef enum { F_SINC, F_IIR, F_BIQUAD } FilterKind;

typedef struct {
    Component base;
    FilterKind kind;
    Wire *out;
    WireList inputs;
    double level_scale;
    double sample_rate;

    // iir state
    double *a, *b;
    double *x, *y;
    size_t na, nb;
    size_t x0, y0;

    // sinc state
    double sinc_freq;
    bool sinc_midi;
    double *sinc_buf;
    int sinc_buf_len;
    int sinc_buf_idx;
    double sinc_sum;
    bool sinc_filling;

    // biquad state
    double bq_freq, bq_q;
    double bq_freq_offset, bq_q_offset;
    double bq_freq_amp, bq_freq_range;
    double bq_q_amp, bq_q_range;
    bool mod_freq, mod_q;
    WireList freq_inputs, q_inputs;
} Filter;

static Wire *filter_output(Component *self) { return ((Filter *)self)->out; }

static void filter_add_input(Component *self, const char *sel, Wire *w) {
    Filter *f = (Filter *)self;
    if (sel && !strcmp(sel, "freq"))
        wirelist_add(&f->freq_inputs, w);
    else if (sel && !strcmp(sel, "Q"))
        wirelist_add(&f->q_inputs, w);
    else
        wirelist_add(&f->inputs, w);
}

// --- sinc ---

static void sinc_reset(Filter *f, Arena *a) {
    if (f->sinc_freq > 0) {
        f->sinc_buf_len = (int)(f->sample_rate / f->sinc_freq);
        f->sinc_buf = arena_alloc(a, (size_t)f->sinc_buf_len * sizeof(double));
        f->sinc_buf_idx = 0;
        f->sinc_sum = 0;
        f->sinc_filling = true;
    } else {
        f->sinc_buf = NULL;
    }
}

static double sinc_next_average(Filter *f, double n) {
    if (!f->sinc_buf) return n;
    if (!f->sinc_filling) f->sinc_sum -= f->sinc_buf[f->sinc_buf_idx];
    if (f->sinc_buf_idx == f->sinc_buf_len - 1) f->sinc_filling = false;
    f->sinc_sum += n;
    f->sinc_buf[f->sinc_buf_idx] = n;
    f->sinc_buf_idx = (f->sinc_buf_idx + 1) % f->sinc_buf_len;
    if (f->sinc_filling) return 0;
    return f->sinc_sum / (double)f->sinc_buf_len;
}

static double filter_compute_sinc(void *ctx) {
    Filter *f = ctx;
    double x = wirelist_sum(&f->inputs);
    return sinc_next_average(f, f->level_scale * x);
}

// --- iir (direct form I, circular x/y) ---

static double filter_compute_iir(void *ctx) {
    Filter *f = ctx;
    double x = wirelist_sum(&f->inputs);
    f->x[f->x0] = x;
    double sigma = 0;
    for (size_t i = 0; i < f->nb; i++)
        sigma += f->b[i] * f->x[(f->na + f->x0 - i) % f->na];
    f->x0 = (f->x0 + 1) % f->na;
    for (size_t i = 1; i < f->na; i++)
        sigma -= f->a[i] * f->y[(f->nb + f->y0 - i) % f->nb];
    f->y[f->y0] = sigma;
    f->y0 = (f->y0 + 1) % f->nb;
    return sigma * f->level_scale;
}

// --- biquad ---

static void bq_set_coefficients(Filter *f, double freq, double q) {
    if (q <= 0) q = 0.5;
    double omega = 2 * M_PI * (freq / f->sample_rate);
    double alpha = sin(omega) * sinh(0.5 / q);

    f->a[0] = 1.0 + alpha;
    f->a[1] = -2.0 * cos(omega);
    f->a[2] = 1.0 - alpha;

    f->b[1] = 1 - cos(omega);
    f->b[0] = 0.5 * f->b[1];
    f->b[2] = f->b[0];

    if (f->a[0] <= 0 || f->a[2] >= 1.0 || (1 + f->a[2]) <= fabs(f->a[1])) {
        for (int i = 0; i < 3; i++) {
            f->a[i] = 1;
            f->b[i] = 1;
        }
        return;
    }
    double a0r = 1.0 / f->a[0];
    f->a[0] = 1;
    f->a[1] *= a0r;
    f->a[2] *= a0r;
    for (int i = 0; i < 3; i++) f->b[i] *= a0r;
}

static double filter_compute_biquad(void *ctx) {
    Filter *f = ctx;
    bool dirty = false;
    if (f->mod_freq) {
        double s = wirelist_sum(&f->freq_inputs);
        double off = f->bq_freq_range * s * 32767.0 / f->bq_freq_amp;
        if (off != f->bq_freq_offset) {
            f->bq_freq_offset = off;
            dirty = true;
        }
    }
    if (f->mod_q) {
        double s = wirelist_sum(&f->q_inputs);
        double off = f->bq_q_range * s * 32767.0 / f->bq_q_amp;
        if (off != f->bq_q_offset) {
            f->bq_q_offset = off;
            dirty = true;
        }
    }
    if (dirty)
        bq_set_coefficients(f, f->bq_freq + f->bq_freq_offset,
                            f->bq_q + f->bq_q_offset);

    double x = wirelist_sum(&f->inputs);
    f->x[0] = x;
    double y0 = f->b[0] * f->x[0] + f->b[1] * f->x[1] + f->b[2] * f->x[2] -
                f->a[1] * f->y[1] - f->a[2] * f->y[2];
    f->x[2] = f->x[1];
    f->x[1] = f->x[0];
    f->y[2] = f->y[1];
    f->y[1] = y0;
    f->y[0] = y0;
    return y0 * f->level_scale;
}

// FilterImpl wraps Filter with the voice arena, needed when a sinc filter
// retunes on note-on and must reallocate its running-average buffer.
typedef struct {
    Filter f;
    Arena *arena;
} FilterImpl;

static void filter_on_midi(Component *self, MidiMsg m) {
    Filter *f = (Filter *)self;
    if (f->kind == F_SINC && f->sinc_midi && midi_is_note_on(m)) {
        f->sinc_freq = 440 * pow(2, ((double)m.data1 - 69) / 12);
        sinc_reset(f, ((FilterImpl *)f)->arena);
    }
}

static int filter_configure(Component *self, const Spec *spec, Voice *v,
                            const char *name) {
    (void)name;
    Filter *f = (Filter *)self;
    Arena *a = voice_arena(v);
    ((FilterImpl *)f)->arena = a;
    wirelist_init(&f->inputs, a);
    wirelist_init(&f->freq_inputs, a);
    wirelist_init(&f->q_inputs, a);
    f->level_scale = 1;
    f->sample_rate = (double)synth_sample_rate(voice_synth(v));

    const char *shape = spec_str(spec, "shape", "sinc");
    if (!strcmp(shape, "") || !strcmp(shape, "sinc")) {
        f->kind = F_SINC;
        double fr;
        if (spec_num(spec, "freq", &fr)) f->sinc_freq = fr;
        if (spec_get(spec, "midi")) f->sinc_midi = true;
        sinc_reset(f, a);
        f->out = voice_new_wire(v, filter_compute_sinc, f);
    } else if (!strcmp(shape, "iir")) {
        f->kind = F_IIR;
        const char *key = spec_get_str(spec, "key");
        if (!key) return -1;
        const double *ca, *cb;
        size_t na, nb;
        if (!iir_spec(key, &ca, &na, &cb, &nb)) return -1;
        f->a = arena_alloc(a, na * sizeof(double));
        f->b = arena_alloc(a, nb * sizeof(double));
        memcpy(f->a, ca, na * sizeof(double));
        memcpy(f->b, cb, nb * sizeof(double));
        f->na = na;
        f->nb = nb;
        f->x = arena_alloc(a, na * sizeof(double));
        f->y = arena_alloc(a, nb * sizeof(double));
        f->out = voice_new_wire(v, filter_compute_iir, f);
    } else if (!strcmp(shape, "biquad")) {
        f->kind = F_BIQUAD;
        f->a = arena_alloc(a, 3 * sizeof(double));
        f->b = arena_alloc(a, 3 * sizeof(double));
        f->x = arena_alloc(a, 3 * sizeof(double));
        f->y = arena_alloc(a, 3 * sizeof(double));
        f->na = f->nb = 3;
        double fr, q;
        if (spec_num(spec, "freq", &fr)) f->bq_freq = fr;
        if (spec_num(spec, "Q", &q)) f->bq_q = q;
        const Spec *mf = spec_get(spec, "input-freq");
        double amp, rg;
        if (spec_is_map(mf) && spec_num(mf, "amp", &amp) &&
            spec_num(mf, "range", &rg)) {
            f->bq_freq_amp = amp;
            f->bq_freq_range = rg;
            f->mod_freq = true;
        }
        const Spec *mq = spec_get(spec, "input-Q");
        if (spec_is_map(mq) && spec_num(mq, "amp", &amp) &&
            spec_num(mq, "range", &rg)) {
            f->bq_q_amp = amp;
            f->bq_q_range = rg;
            f->mod_q = true;
        }
        bq_set_coefficients(f, f->bq_freq, f->bq_q);
        f->out = voice_new_wire(v, filter_compute_biquad, f);
    } else {
        return -1;
    }

    double ls;
    if (spec_num(spec, "level-scale", &ls)) f->level_scale = ls;
    return 0;
}

static const ComponentVTable FILTER_VT = {
    .configure = filter_configure,
    .output = filter_output,
    .add_input = filter_add_input,
    .on_midi = filter_on_midi,
};

Component *filter_new(Arena *a) {
    FilterImpl *fi = arena_alloc(a, sizeof(*fi));
    fi->f.base.vt = &FILTER_VT;
    return &fi->f.base;
}
