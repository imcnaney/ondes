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

// synth_set_pool_enabled turns on voice-graph recycling: instead of
// building and freeing a voice per note, finished voices are reset and
// returned to a per-patch pool, so note-on reuses a pre-built graph rather
// than re-running patch.Apply on the audio thread. Off by default (the
// fresh-per-note path). Set before play begins.
void synth_set_pool_enabled(Synth *s, bool enabled);

// synth_pool_size reports how many voices have been built into pools (for
// tests/diagnostics).
int synth_pool_size(const Synth *s);

// synth_reserve pre-sizes the internal voice bookkeeping arrays for up to
// max_voices simultaneous voices, so note-on/off never reallocate on the
// audio thread. Call once before play.
void synth_reserve(Synth *s, int max_voices);

// synth_prewarm builds n_per_patch voice graphs for the default patch and
// each per-channel patch up front, parking them in the idle pool. Combined
// with pooling and synth_reserve this makes a steady-state note-on
// allocation-free (no voice_new/Apply on the audio thread). Requires
// pooling enabled; call once before play, on a non-audio thread.
void synth_prewarm(Synth *s, int n_per_patch);

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
