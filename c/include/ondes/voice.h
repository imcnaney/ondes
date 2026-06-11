// Voice is one instantiation of a patch, currently playing one MIDI note.
// It owns a self-contained per-(channel,note) audio graph: its components,
// wires, phase clocks, and a main summing junction, all allocated from the
// voice's arena so teardown is a single arena_free.
#ifndef ONDES_VOICE_H
#define ONDES_VOICE_H

#include <stdbool.h>
#include <stdint.h>

#include "ondes/arena.h"
#include "ondes/instant.h"
#include "ondes/midi.h"
#include "ondes/wire.h"

typedef struct Synth Synth;
typedef struct Component Component; // defined in component.h

// Junction sums any number of input wires into a single output wire.
typedef struct Junction {
    Arena *arena;
    Wire **inputs;
    size_t n_inputs, cap_inputs;
    Wire *out;
} Junction;

typedef struct Voice {
    uint8_t note;
    uint8_t chan;
    uint8_t velocity;

    Synth *synth;
    Arena arena;

    struct {
        char *name;
        Component *c;
    } *comps;
    size_t n_comps, cap_comps;

    Wire **wires;
    size_t n_wires, cap_wires;

    PhaseClock **clocks; // phase clocks this voice owns, released on teardown
    size_t n_clocks, cap_clocks;

    Junction *voice_mix;

    bool wait_for_env; // if true, note-off does not immediately remove voice

    // draining is set once an exit-envelope has reached zero: the voice is
    // finished at the source but downstream effects (echo) may still ring.
    // While draining it stays in the mix until silent for endingZeros
    // consecutive samples.
    bool draining;
    int zero_count;

    // Pooling: snapshot is the byte-exact post-Apply arena image used to
    // reset the voice for reuse; pool is an opaque back-reference to the
    // idle pool this voice returns to (NULL for non-pooled voices).
    ArenaSnapshot *snapshot;
    void *pool;
} Voice;

// voice_new allocates a fresh voice with its main mix junction.
Voice *voice_new(Synth *s, uint8_t ch, uint8_t note, uint8_t vel);
// voice_free releases the voice arena and the voice itself. Clocks must be
// released first via voice_release_clocks (the synth does this).
void voice_free(Voice *v);

Synth *voice_synth(Voice *v);
Arena *voice_arena(Voice *v);

// voice_note_freq returns the equal-tempered frequency for the MIDI note.
double voice_note_freq(const Voice *v);

// voice_new_wire allocates a wire from the voice arena and registers it so
// it is reset each sample.
Wire *voice_new_wire(Voice *v, double (*compute)(void *), void *ctx);

// voice_add_phase_clock creates a phase clock owned by this voice.
PhaseClock *voice_add_phase_clock(Voice *v);
// voice_release_clocks unregisters this voice's clocks from the Instant.
void voice_release_clocks(Voice *v);

// voice_add_voice_mix_input plugs a wire into the main summing junction
// (used when a component declares `out: main`).
void voice_add_voice_mix_input(Voice *v, Wire *w);
// voice_main_output is the voice's contribution to the main mix.
Wire *voice_main_output(Voice *v);

void voice_reset_wires(Voice *v);

void voice_add_component(Voice *v, const char *name, Component *c);
Component *voice_component(Voice *v, const char *name);

// voice_dispatch_midi delivers a channel message to every component that
// listens (implements on_midi).
void voice_dispatch_midi(Voice *v, MidiMsg m);

void voice_set_wait_for_env(Voice *v, bool b);
bool voice_wait_for_env(const Voice *v);
void voice_start_draining(Voice *v);

// voice_build_snapshot captures the post-Apply arena image for recycling.
// Call once, after the patch has been applied and before the first note.
void voice_build_snapshot(Voice *v);

// voice_reset_for_reuse restores the snapshot (resetting every in-arena
// component to its post-Apply state), resets out-of-arena state (phase
// clocks, via each component's reset), and re-stamps the voice for a new
// note. The wire graph and component wiring are untouched.
void voice_reset_for_reuse(Voice *v, uint8_t ch, uint8_t note, uint8_t vel);

// Junction helpers (also used by the mix component).
Junction *junction_new(Voice *v);
void junction_add_input(Junction *j, Wire *w);
Wire *junction_output(Junction *j);

#endif // ONDES_VOICE_H
