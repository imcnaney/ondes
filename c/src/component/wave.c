// Oscillator component: simple periodic shapes (sine/saw/square/ramp),
// additive harmonic stacks, anharmonic stacks (one phase clock per
// non-integer partial), PWM, and colored noise. Ported from
// component/wave/wave.go.
#include <math.h>
#include <stdint.h>
#include <string.h>

#include "computil.h"
#include "ondes/component.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

typedef enum {
    SH_SINE,
    SH_SAW,
    SH_SQUARE,
    SH_RAMPUP,
    SH_RAMPDOWN,
    SH_HARMONIC,
    SH_ANHARMONIC,
    SH_NOISE,
    SH_PINK,
    SH_PWM,
} Shape;

typedef struct {
    uint64_t rng;
    double latch[2]; // hold lengths 1 and 23 samples
    double last;
    int64_t n;
    bool pink;
} NoiseState;

typedef struct {
    Component base;
    Shape shape;
    Voice *voice;
    PhaseClock *clock; // fundamental
    PhaseClock **extra;
    double *mults; // frequency multipliers for extra clocks
    size_t n_extra;
    Wire *out;

    double level;
    double freq_mul; // 2^(offset/12) * 2^(detune/1200)
    double base_freq;
    bool fixed_freq;
    bool unsign; // unipolar output when `signed: false`
    double vel_base, vel_amount, vel_gain;

    WireList pwm_in;
    WireList log_in;
    double log_amp_inv, log_mod_exp;

    // harmonic / anharmonic params (alternating mult, divisor)
    double *harm;
    size_t n_harm;
    double *anharm;
    size_t n_anharm;

    // pwm
    double pwm_mod_percent, pwm_input_amp_inv;

    NoiseState noise;
} Wave;

// waveDefaultLevel is the empirical scale that makes a bare sine patch
// produce roughly the same RMS as Java's reference.
static const double WAVE_DEFAULT_LEVEL = 0.025;

static const struct {
    const char *name;
    Shape shape;
} SHAPES[] = {
    {"sine", SH_SINE},          {"saw", SH_SAW},
    {"square", SH_SQUARE},      {"ramp-up", SH_RAMPUP},
    {"ramp-down", SH_RAMPDOWN}, {"harmonic", SH_HARMONIC},
    {"anharmonic", SH_ANHARMONIC}, {"noise", SH_NOISE},
    {"pink", SH_PINK},          {"pwm", SH_PWM},
};

static bool lookup_shape(const char *name, Shape *out) {
    for (size_t i = 0; i < sizeof(SHAPES) / sizeof(SHAPES[0]); i++)
        if (strcmp(SHAPES[i].name, name) == 0) {
            *out = SHAPES[i].shape;
            return true;
        }
    return false;
}

// harmonicPresets mirrors HarmonicWaveGen.presets: alternating
// (frequency-multiplier, amplitude-divisor) pairs.
static bool preset_params(const char *name, const double **out, size_t *n) {
    static const double mellow[] = {1, 1, 2, 2, 3, 3};
    static const double odd[] = {1, 1, 2, 2, 6, 3, 14, 3};
    static const double bell[] = {1, 1, 2, 2, 11, 3, 14, 3, 17, 3};
    static const double organ[] = {1, 1, 2, 2, 3, 3, 4, 2, 8, 2, 12, 3};
    if (!strcmp(name, "mellow")) { *out = mellow; *n = 6; return true; }
    if (!strcmp(name, "odd")) { *out = odd; *n = 8; return true; }
    if (!strcmp(name, "bell")) { *out = bell; *n = 10; return true; }
    if (!strcmp(name, "organ")) { *out = organ; *n = 12; return true; }
    return false;
}

// parse_wave_params reads `preset:` or `waves:` into a flat even-length
// array of doubles in the voice arena.
static double *parse_wave_params(Wave *w, const Spec *spec, size_t *n) {
    *n = 0;
    const char *preset = spec_get_str(spec, "preset");
    if (preset) {
        const double *p;
        size_t pn;
        if (!preset_params(preset, &p, &pn)) return NULL;
        double *out = arena_alloc(voice_arena(w->voice), pn * sizeof(double));
        memcpy(out, p, pn * sizeof(double));
        *n = pn;
        return out;
    }
    const Spec *waves = spec_get(spec, "waves");
    if (!waves) return NULL;
    bool ok = true;
    double *out = spec_tokens_doubles(voice_arena(w->voice), waves, n, &ok);
    if (!ok || *n == 0 || (*n % 2) != 0) {
        *n = 0;
        return NULL;
    }
    return out;
}

static double noise_rand(NoiseState *ns) {
    uint64_t x = ns->rng;
    x ^= x << 13;
    x ^= x >> 7;
    x ^= x << 17;
    ns->rng = x;
    return (double)(x >> 11) * (1.0 / 9007199254740992.0);
}

static double noise_next(NoiseState *ns) {
    ns->n++;
    if (ns->n % 1 == 0) ns->latch[0] = noise_rand(ns) * 2 - 1;
    if (ns->n % 23 == 0) ns->latch[1] = noise_rand(ns) * 2 - 1;
    double mean = (ns->latch[0] + ns->latch[1]) / 2.0;
    if (!ns->pink) {
        ns->last = ns->last + (mean - ns->last) / 20.0;
    } else {
        const double min_step = 3.0 / 1024.0;
        double diff = mean - ns->last;
        if (diff > 0)
            ns->last += fmax(diff, min_step);
        else if (diff < 0)
            ns->last -= fmax(-diff, min_step);
    }
    return ns->last * 3.0;
}

static double wave_named_sum(WireList *l) { return wirelist_sum(l); }

// modFreq applies log frequency modulation to the fundamental clock (and
// any anharmonic-partial clocks).
static void wave_mod_freq(Wave *w) {
    if (w->log_mod_exp == 0) return;
    double log_inp = wave_named_sum(&w->log_in) * w->log_amp_inv * w->log_mod_exp;
    double freq = w->base_freq * pow(2, log_inp);
    phase_clock_set_frequency(w->clock, freq);
    for (size_t i = 0; i < w->n_extra; i++)
        phase_clock_set_frequency(w->extra[i], freq * w->mults[i]);
}

static double wave_gen(Wave *w) {
    double phase = phase_clock_phase(w->clock);
    switch (w->shape) {
    case SH_SINE:
        return sin(2 * M_PI * phase);
    case SH_SAW: // misleadingly named: actually a triangle wave
        return phase < 0.5 ? 4 * phase - 1 : 4 * (1 - phase) - 1;
    case SH_SQUARE:
        return phase > 0.5 ? 1 : -1;
    case SH_RAMPUP:
        return 2 * phase - 1;
    case SH_RAMPDOWN:
        return 2 * (1 - phase) - 1;
    case SH_HARMONIC: {
        double sum = 0;
        for (size_t i = 0; i + 1 < w->n_harm; i += 2)
            sum += sin(2 * M_PI * phase * w->harm[i]) / w->harm[i + 1];
        return sum;
    }
    case SH_ANHARMONIC: {
        double sum = 0;
        for (size_t i = 0; i + 1 < w->n_harm; i += 2)
            sum += sin(2 * M_PI * phase * w->harm[i]) / w->harm[i + 1];
        for (size_t i = 0; i + 1 < w->n_anharm; i += 2)
            sum += sin(2 * M_PI * phase_clock_phase(w->extra[i / 2])) /
                   w->anharm[i + 1];
        return sum;
    }
    case SH_NOISE:
    case SH_PINK:
        return noise_next(&w->noise);
    case SH_PWM: {
        double mod = wave_named_sum(&w->pwm_in) * w->pwm_input_amp_inv;
        double duty = 0.5 + (w->pwm_mod_percent / 200.0) * mod;
        return phase_clock_phase(w->clock) > duty ? 1 : -1;
    }
    }
    return 0;
}

static double wave_sample(void *ctx) {
    Wave *w = ctx;
    wave_mod_freq(w);
    double v = wave_gen(w);
    if (w->unsign) {
        if (w->shape == SH_RAMPUP || w->shape == SH_RAMPDOWN)
            v = (v + 1) * 0.5;
        else
            v += 1;
    }
    return v * w->level * w->vel_gain;
}

static void wave_retune(Wave *w, double midi_key) {
    w->base_freq = 440 * pow(2, (midi_key - 69) / 12) * w->freq_mul;
    phase_clock_set_frequency(w->clock, w->base_freq);
    phase_clock_reset_phase(w->clock);
    for (size_t i = 0; i < w->n_extra; i++) {
        phase_clock_set_frequency(w->extra[i], w->base_freq * w->mults[i]);
        phase_clock_reset_phase(w->extra[i]);
    }
}

static void wave_on_midi(Component *self, MidiMsg m) {
    Wave *w = (Wave *)self;
    if (midi_is_note_on(m) && !w->fixed_freq) {
        wave_retune(w, (double)m.data1);
        w->vel_gain = w->vel_base + w->vel_amount * (double)m.data2 / 128;
        if (w->vel_gain > 1) w->vel_gain = 1;
    }
}

static void wave_add_input(Component *self, const char *sel, Wire *src) {
    Wave *w = (Wave *)self;
    if (sel && strcmp(sel, "pwm") == 0)
        wirelist_add(&w->pwm_in, src);
    else if (sel && strcmp(sel, "log") == 0)
        wirelist_add(&w->log_in, src);
    // basic shapes ignore other pins
}

static Wire *wave_output(Component *self) { return ((Wave *)self)->out; }

static int wave_configure(Component *self, const Spec *spec, Voice *v,
                          const char *name) {
    (void)name;
    Wave *w = (Wave *)self;
    Arena *a = voice_arena(v);
    w->voice = v;
    wirelist_init(&w->pwm_in, a);
    wirelist_init(&w->log_in, a);

    const char *shape = spec_get_str(spec, "shape");
    if (!shape || !lookup_shape(shape, &w->shape)) return -1;

    w->freq_mul = 1;
    w->vel_amount = 1;
    w->vel_gain = 1;
    double tmp;
    if (spec_num(spec, "velocity-base", &tmp)) w->vel_base = tmp / 100;
    if (spec_num(spec, "velocity-amount", &tmp)) w->vel_amount = tmp / 100;
    if (spec_num(spec, "offset", &tmp)) w->freq_mul *= pow(2, tmp / 12);
    if (spec_num(spec, "detune", &tmp)) w->freq_mul *= pow(2, tmp / 1200);
    w->unsign = !spec_bool(spec, "signed", true);

    w->clock = voice_add_phase_clock(v);
    if (spec_num(spec, "freq", &tmp)) {
        w->fixed_freq = true;
        w->base_freq = tmp * w->freq_mul;
    } else {
        w->base_freq = voice_note_freq(v) * w->freq_mul;
    }
    phase_clock_set_frequency(w->clock, w->base_freq);

    const Spec *il = spec_get(spec, "input-log");
    if (spec_is_map(il)) {
        double amp = spec_double(il, "amp", 0);
        double semis = spec_double(il, "semitones", 0);
        if (amp > 0) {
            w->log_amp_inv = 32767.0 / amp;
            w->log_mod_exp = semis / 12.0;
        }
    }

    if (w->shape == SH_HARMONIC || w->shape == SH_ANHARMONIC) {
        size_t pn;
        double *params = parse_wave_params(w, spec, &pn);
        if (!params) return -1;
        if (w->shape == SH_HARMONIC) {
            w->harm = params;
            w->n_harm = pn;
        } else {
            // Split integer multipliers (harmonic) from non-integer
            // (anharmonic, each gets its own phase clock).
            w->harm = arena_alloc(a, pn * sizeof(double));
            w->anharm = arena_alloc(a, pn * sizeof(double));
            for (size_t i = 0; i + 1 < pn; i += 2) {
                if (fmod(params[i], 1.0) == 0) {
                    w->harm[w->n_harm++] = params[i];
                    w->harm[w->n_harm++] = params[i + 1];
                } else {
                    w->anharm[w->n_anharm++] = params[i];
                    w->anharm[w->n_anharm++] = params[i + 1];
                }
            }
            size_t ne = w->n_anharm / 2;
            w->extra = arena_alloc(a, ne * sizeof(*w->extra));
            w->mults = arena_alloc(a, ne * sizeof(*w->mults));
            for (size_t i = 0; i + 1 < w->n_anharm; i += 2) {
                PhaseClock *c = voice_add_phase_clock(v);
                phase_clock_set_frequency(c, w->base_freq * w->anharm[i]);
                w->extra[w->n_extra] = c;
                w->mults[w->n_extra] = w->anharm[i];
                w->n_extra++;
            }
        }
    } else if (w->shape == SH_NOISE || w->shape == SH_PINK) {
        static uint64_t seed_counter;
        seed_counter += 0x9e3779b97f4a7c15ULL;
        w->noise.rng = seed_counter ^ (uint64_t)(uintptr_t)w ^ 0xD1B54A32D192ED03ULL;
        if (w->noise.rng == 0) w->noise.rng = 1;
        w->noise.pink = (w->shape == SH_PINK);
    } else if (w->shape == SH_PWM) {
        double mp;
        if (spec_num(spec, "mod-percent", &mp) && mp >= 0 && mp <= 100)
            w->pwm_mod_percent = mp;
        double ia;
        if (spec_num(spec, "input-amp", &ia) && ia != 0)
            w->pwm_input_amp_inv = 32767.0 / ia;
    }

    if (spec_num(spec, "level-override", &tmp))
        w->level = tmp / 32767.0;
    else
        w->level = WAVE_DEFAULT_LEVEL;
    if (spec_num(spec, "level-scale", &tmp)) w->level *= tmp;

    w->out = voice_new_wire(v, wave_sample, w);
    return 0;
}

static const ComponentVTable WAVE_VT = {
    .configure = wave_configure,
    .output = wave_output,
    .add_input = wave_add_input,
    .on_midi = wave_on_midi,
    .named_output = NULL,
};

Component *wave_new(Arena *a) {
    Wave *w = arena_alloc(a, sizeof(*w));
    w->base.vt = &WAVE_VT;
    return &w->base;
}
