#include "ondes/synth.h"

#include <math.h>
#include <stdio.h>
#include <stdlib.h>

// silenceThreshold is the voice-output magnitude below which a sample
// rounds to int16 zero. endingZeros is how many consecutive such samples
// mark a draining voice as finished - matching Java's WaveMonoMainMix,
// which stops the render after 100 zero output samples.
#define SILENCE_THRESHOLD (1.0 / 32767.0)
#define ENDING_ZEROS 100

typedef struct {
    uint8_t ch, note;
} PendingEnd;

struct Synth {
    int sr;
    Instant *instant;
    Limiter *limiter;

    SynthPatch default_patch;
    SynthPatch patches[16]; // per-channel override; ctx==NULL means unset

    Voice *table[16][128]; // lookup by (channel, note)
    Voice **active;        // iteration over live voices
    size_t n_active, cap_active;

    int16_t cc[16][128]; // most-recent CC value per (channel, controller), -1 unset

    PendingEnd *pending;
    size_t n_pending, cap_pending;

    bool apply_err_logged;
};

Synth *synth_new(int sr, SynthPatch default_patch) {
    Synth *s = calloc(1, sizeof(*s));
    s->sr = sr;
    s->instant = instant_new(sr);
    s->limiter = limiter_new(sr);
    s->default_patch = default_patch;
    for (int c = 0; c < 16; c++)
        for (int n = 0; n < 128; n++) s->cc[c][n] = -1;
    return s;
}

void synth_free(Synth *s) {
    if (!s) return;
    for (size_t i = 0; i < s->n_active; i++) {
        voice_release_clocks(s->active[i]);
        voice_free(s->active[i]);
    }
    free(s->active);
    free(s->pending);
    limiter_free(s->limiter);
    instant_free(s->instant);
    free(s);
}

int synth_sample_rate(const Synth *s) { return s->sr; }
Instant *synth_instant(Synth *s) { return s->instant; }
int synth_active_voices(const Synth *s) { return (int)s->n_active; }

void synth_set_channel_patch(Synth *s, uint8_t ch, SynthPatch p) {
    if (ch < 16) s->patches[ch] = p;
}

static SynthPatch patch_for(Synth *s, uint8_t ch) {
    if (ch < 16 && s->patches[ch].ctx) return s->patches[ch];
    return s->default_patch;
}

static void active_add(Synth *s, Voice *v) {
    if (s->n_active == s->cap_active) {
        size_t cap = s->cap_active ? s->cap_active * 2 : 16;
        s->active = realloc(s->active, cap * sizeof(*s->active));
        s->cap_active = cap;
    }
    s->active[s->n_active++] = v;
}

static void active_remove(Synth *s, Voice *v) {
    for (size_t i = 0; i < s->n_active; i++) {
        if (s->active[i] == v) {
            s->active[i] = s->active[s->n_active - 1];
            s->n_active--;
            return;
        }
    }
}

static void remove_voice(Synth *s, uint8_t ch, uint8_t note) {
    Voice *v = s->table[ch][note];
    if (!v) return;
    voice_release_clocks(v); // stop the Instant ticking a dead voice's clocks
    active_remove(s, v);
    s->table[ch][note] = NULL;
    voice_free(v);
}

void synth_note_on(Synth *s, uint8_t ch, uint8_t note, uint8_t vel) {
    ch &= 0x0F;
    if (note > 127) return;
    Voice *existing = s->table[ch][note];
    if (existing) {
        // Retrigger: keep the voice, cancel any drain so a re-struck note
        // that was tailing out plays in full again.
        existing->velocity = vel;
        existing->draining = false;
        existing->zero_count = 0;
        voice_dispatch_midi(existing,
                            (MidiMsg){0x90 | ch, note, vel});
        return;
    }
    SynthPatch p = patch_for(s, ch);
    if (!p.ctx) return; // no patch assigned to this channel
    Voice *v = voice_new(s, ch, note, vel);
    if (p.apply(p.ctx, v) != 0) {
        // Patch failed; drop this note. Log once - the same patch fails the
        // same way on every note.
        if (!s->apply_err_logged) {
            fprintf(stderr, "synth: dropping notes, patch failed to apply\n");
            s->apply_err_logged = true;
        }
        voice_free(v);
        return;
    }
    s->table[ch][note] = v;
    active_add(s, v);
    voice_dispatch_midi(v, (MidiMsg){0x90 | ch, note, vel});
    // Replay the channel's current controller state so a voice created
    // mid-sweep starts at the live CC value rather than zero.
    for (int cc = 0; cc < 128; cc++) {
        if (s->cc[ch][cc] >= 0)
            voice_dispatch_midi(
                v, (MidiMsg){0xB0 | ch, (uint8_t)cc, (uint8_t)s->cc[ch][cc]});
    }
}

void synth_control_change(Synth *s, uint8_t ch, uint8_t cc, uint8_t val) {
    ch &= 0x0F;
    if (cc > 127) return;
    s->cc[ch][cc] = (int16_t)val;
    MidiMsg m = {0xB0 | ch, cc, val};
    for (size_t i = 0; i < s->n_active; i++)
        if (s->active[i]->chan == ch) voice_dispatch_midi(s->active[i], m);
}

void synth_note_off(Synth *s, uint8_t ch, uint8_t note) {
    ch &= 0x0F;
    if (note > 127) return;
    Voice *v = s->table[ch][note];
    if (!v) return;
    voice_dispatch_midi(v, (MidiMsg){0x80 | ch, note, 0});
    if (voice_wait_for_env(v)) {
        // The envelope will queue removal when its release phase finishes.
        return;
    }
    remove_voice(s, ch, note);
}

void synth_queue_note_end(Synth *s, uint8_t ch, uint8_t note) {
    ch &= 0x0F;
    if (note > 127) return;
    Voice *v = s->table[ch][note];
    if (v) voice_start_draining(v);
}

static void pending_add(Synth *s, uint8_t ch, uint8_t note) {
    if (s->n_pending == s->cap_pending) {
        size_t cap = s->cap_pending ? s->cap_pending * 2 : 8;
        s->pending = realloc(s->pending, cap * sizeof(*s->pending));
        s->cap_pending = cap;
    }
    s->pending[s->n_pending++] = (PendingEnd){ch, note};
}

double synth_step(Synth *s) {
    instant_next(s->instant);
    for (size_t i = 0; i < s->n_active; i++) voice_reset_wires(s->active[i]);

    double sum = 0;
    for (size_t i = 0; i < s->n_active; i++) {
        Voice *v = s->active[i];
        // The wire latches per sample, so reading the voice output here
        // returns the same value summed into the mix; no recomputation.
        double out = wire_get(voice_main_output(v));
        sum += out;
        if (!v->draining) continue;
        if (fabs(out) < SILENCE_THRESHOLD)
            v->zero_count++;
        else
            v->zero_count = 0;
        if (v->zero_count > ENDING_ZEROS) pending_add(s, v->chan, v->note);
    }
    for (size_t i = 0; i < s->n_pending; i++)
        remove_voice(s, s->pending[i].ch, s->pending[i].note);
    s->n_pending = 0;
    return limiter_apply(s->limiter, sum);
}
