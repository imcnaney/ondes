// `midi-note`: emits per-note frequency (linear-out) or note number
// (log-out) onto a named pin of another component. Ported from
// component/midinote/midinote.go. Wiring is via named outputs, resolved
// in the patch post-pass.
#include <math.h>
#include <string.h>

#include "computil.h"
#include "ondes/component.h"

typedef struct {
    Component base;
    Wire *linear_out;
    Wire *log_out;
    double linear_amp, log_amp;
    double scaled_linear, scaled_log;
} MidiNote;

// FreqTable bounds, matching Java FreqTable.getFreq(0)..getFreq(127).
static double min_freq(void) { return 440 * pow(2, (0 - 69) / 12.0); }
static double max_freq(void) { return 440 * pow(2, (127 - 69) / 12.0); }

static double mn_compute_linear(void *ctx) { return ((MidiNote *)ctx)->scaled_linear; }
static double mn_compute_log(void *ctx) { return ((MidiNote *)ctx)->scaled_log; }

static Wire *mn_output(Component *self) {
    (void)self;
    return NULL; // wiring is via named outputs only
}

static Wire *mn_named_output(Component *self, const char *key) {
    MidiNote *m = (MidiNote *)self;
    if (!strcmp(key, "linear-out")) return m->linear_out;
    if (!strcmp(key, "log-out")) return m->log_out;
    return NULL;
}

static void mn_on_midi(Component *self, MidiMsg msg) {
    MidiNote *m = (MidiNote *)self;
    if (!midi_is_note_on(msg)) return;
    double note = (double)msg.data1;
    double freq = 440 * pow(2, (note - 69) / 12);
    if (m->linear_out)
        m->scaled_linear =
            freq / (max_freq() - min_freq()) * m->linear_amp / 32767.0;
    if (m->log_out) m->scaled_log = (note / 128) * m->log_amp / 32767.0;
}

static int mn_configure(Component *self, const Spec *spec, Voice *v,
                        const char *name) {
    (void)name;
    MidiNote *m = (MidiNote *)self;
    const Spec *lin = spec_get(spec, "linear-out");
    if (spec_is_map(lin)) {
        double amp;
        if (spec_num(lin, "amp", &amp)) {
            m->linear_amp = amp;
            m->linear_out = voice_new_wire(v, mn_compute_linear, m);
        }
    }
    const Spec *lg = spec_get(spec, "log-out");
    if (spec_is_map(lg)) {
        double amp;
        if (spec_num(lg, "amp", &amp)) {
            m->log_amp = amp;
            m->log_out = voice_new_wire(v, mn_compute_log, m);
        }
    }
    return 0;
}

static const ComponentVTable MIDINOTE_VT = {
    .configure = mn_configure,
    .output = mn_output,
    .on_midi = mn_on_midi,
    .named_output = mn_named_output,
};

Component *midinote_new(Arena *a) {
    MidiNote *m = arena_alloc(a, sizeof(*m));
    m->base.vt = &MIDINOTE_VT;
    return &m->base;
}
