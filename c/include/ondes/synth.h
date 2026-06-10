// Synth is the audio engine: sample clock, active voices, main mix
// accumulator and limiter. It is multi-timbral - each MIDI channel plays
// its own patch (a default patch covers unassigned channels) and voices
// are keyed by (channel, note) so the same note on two channels is two
// distinct voices.
#ifndef ONDES_SYNTH_H
#define ONDES_SYNTH_H

#include <stdbool.h>
#include <stdint.h>

#include "ondes/instant.h"
#include "ondes/limiter.h"
#include "ondes/midi.h"
#include "ondes/voice.h"

// A Patch instantiates its components into a fresh voice. apply returns 0
// on success, non-zero on failure (e.g. unknown component type). ctx==NULL
// means "no patch assigned".
typedef int (*PatchApplyFn)(void *ctx, Voice *v);
typedef struct SynthPatch {
    void *ctx;
    PatchApplyFn apply;
} SynthPatch;

typedef struct Synth Synth;

Synth *synth_new(int sr, SynthPatch default_patch);
void synth_free(Synth *s);

int synth_sample_rate(const Synth *s);
Instant *synth_instant(Synth *s);
int synth_active_voices(const Synth *s);

// synth_set_channel_patch overrides the default patch for one channel.
void synth_set_channel_patch(Synth *s, uint8_t ch, SynthPatch p);

void synth_note_on(Synth *s, uint8_t ch, uint8_t note, uint8_t vel);
void synth_note_off(Synth *s, uint8_t ch, uint8_t note);
void synth_control_change(Synth *s, uint8_t ch, uint8_t cc, uint8_t val);

// synth_queue_note_end is called by a terminal component (env with
// exit:true) when a voice has finished at its source; the voice keeps
// rendering until its effect tail decays to silence, then Step removes it.
void synth_queue_note_end(Synth *s, uint8_t ch, uint8_t note);

// synth_step advances one sample and returns the limited mix in [-1, +1].
double synth_step(Synth *s);

#endif // ONDES_SYNTH_H
