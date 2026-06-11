// o is the live synth: it listens on a MIDI input port and plays the notes
// through a loaded patch to an audio output device in real time. It is the
// C counterpart of the Java `o` script and the Go cmd/o.
//
//   o -in <substr> [-patch name] [-out substr] [-buffer-size n] [-hold]
//   o -list                       list MIDI inputs + audio outputs
//   o -patch chan:name            per-channel patch (multi-timbral)
//   o -selftest                   play a note through the patch, no MIDI in
//
// Threading model (mirrors Java's MidiListenerThread / the Go port): the
// engine is single-threaded and owned exclusively by the audio callback.
// The CoreMIDI callback runs on another thread and never touches the engine
// directly - it pushes commands onto a lock-free single-producer/single-
// consumer ring, which the audio callback drains at the top of each buffer
// before rendering. No lock sits on the per-sample path; on overflow the
// MIDI side drops rather than blocks.
#include <signal.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "ondes/audiodev.h"
#include "ondes/component.h"
#include "ondes/mididev.h"
#include "ondes/patch.h"
#include "ondes/synth.h"
#include "ondes/wav.h"

// --- lock-free SPSC command ring ---

#define RING_CAP 1024
typedef struct {
    uint8_t kind; // 0 note-on, 1 note-off, 2 cc
    uint8_t ch, d1, d2;
} Cmd;

typedef struct {
    Cmd buf[RING_CAP];
    _Atomic unsigned head, tail;
    _Atomic unsigned long dropped;
} Ring;

static void ring_push(Ring *r, Cmd c) {
    unsigned t = atomic_load_explicit(&r->tail, memory_order_relaxed);
    unsigned h = atomic_load_explicit(&r->head, memory_order_acquire);
    unsigned next = (t + 1) % RING_CAP;
    if (next == h) {
        atomic_fetch_add_explicit(&r->dropped, 1, memory_order_relaxed);
        return;
    }
    r->buf[t] = c;
    atomic_store_explicit(&r->tail, next, memory_order_release);
}

static int ring_pop(Ring *r, Cmd *out) {
    unsigned h = atomic_load_explicit(&r->head, memory_order_relaxed);
    unsigned t = atomic_load_explicit(&r->tail, memory_order_acquire);
    if (h == t) return 0;
    *out = r->buf[h];
    atomic_store_explicit(&r->head, (h + 1) % RING_CAP, memory_order_release);
    return 1;
}

// --- live context shared between threads ---

typedef struct {
    Synth *s;
    Ring ring;
    int hold;
} Live;

static void apply_cmd(Synth *s, Cmd c) {
    switch (c.kind) {
    case 0: synth_note_on(s, c.ch, c.d1, c.d2); break;
    case 1: synth_note_off(s, c.ch, c.d1); break;
    case 2: synth_control_change(s, c.ch, c.d1, c.d2); break;
    }
}

// audio thread: drain the ring, then render the period.
static void render(float *out, unsigned frames, void *user) {
    Live *L = user;
    Cmd c;
    while (ring_pop(&L->ring, &c)) apply_cmd(L->s, c);
    for (unsigned i = 0; i < frames; i++) {
        float v = (float)synth_step(L->s);
        out[2 * i] = v;
        out[2 * i + 1] = v;
    }
}

// MIDI thread: classify and enqueue.
static void on_midi(uint8_t status, uint8_t d1, uint8_t d2, void *user) {
    Live *L = user;
    uint8_t hi = status & 0xF0, ch = status & 0x0F;
    Cmd c = {0};
    c.ch = ch;
    c.d1 = d1;
    c.d2 = d2;
    if (hi == 0x90 && d2 > 0) {
        c.kind = 0;
    } else if (hi == 0x80 || (hi == 0x90 && d2 == 0)) {
        if (L->hold) return; // drone: never release
        c.kind = 1;
    } else if (hi == 0xB0) {
        c.kind = 2;
    } else {
        return;
    }
    ring_push(&L->ring, c);
}

// --- patch assignment (-patch name | -patch chan:name) ---

#define MAX_PATCHES 32
typedef struct {
    int ch; // -1 = default
    const char *name;
} Assign;

static volatile sig_atomic_t g_stop;
static void on_sigint(int sig) {
    (void)sig;
    g_stop = 1;
}

static void msleep(long ms) {
    struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
    nanosleep(&ts, NULL);
}

int main(int argc, char **argv) {
    const char *in_sub = NULL, *out_sub = NULL;
    int buffer = 1024, sample_rate = ONDES_SAMPLE_RATE;
    int hold = 0, list = 0, selftest = 0, pool = 0;
    Assign assigns[MAX_PATCHES];
    int n_assign = 0;

    for (int i = 1; i < argc; i++) {
        const char *a = argv[i];
        if (!strcmp(a, "-in") && i + 1 < argc)
            in_sub = argv[++i];
        else if (!strcmp(a, "-out") && i + 1 < argc)
            out_sub = argv[++i];
        else if (!strcmp(a, "-buffer-size") && i + 1 < argc)
            buffer = atoi(argv[++i]);
        else if (!strcmp(a, "-sample-rate") && i + 1 < argc)
            sample_rate = atoi(argv[++i]);
        else if (!strcmp(a, "-hold"))
            hold = 1;
        else if (!strcmp(a, "-list"))
            list = 1;
        else if (!strcmp(a, "-selftest"))
            selftest = 1;
        else if (!strcmp(a, "-pool"))
            pool = 1;
        else if (!strcmp(a, "-patch") && i + 1 < argc && n_assign < MAX_PATCHES) {
            const char *v = argv[++i];
            const char *colon = strchr(v, ':');
            if (colon) {
                int ch = atoi(v);
                if (ch < 1 || ch > 16) {
                    fprintf(stderr, "o: bad channel in %s (want chan:name)\n", v);
                    return 2;
                }
                assigns[n_assign++] = (Assign){ch - 1, colon + 1};
            } else {
                assigns[n_assign++] = (Assign){-1, v};
            }
        } else if (a[0] != '-' && n_assign < MAX_PATCHES) {
            assigns[n_assign++] = (Assign){-1, a}; // bare positional = default
        }
    }

    if (list) {
        audio_list_outputs();
        printf("\n");
        midi_list_inputs();
        return 0;
    }

    if (n_assign == 0) assigns[n_assign++] = (Assign){-1, "sine"};

    component_register_all();

    // Load patches (cache by name so a repeated name loads once).
    Patch *loaded[MAX_PATCHES];
    int n_loaded = 0;
    SynthPatch def = {0};
    SynthPatch by_chan[16];
    int has_chan[16] = {0};
    char perr[256];
    for (int i = 0; i < n_assign; i++) {
        Patch *p = NULL;
        for (int j = 0; j < n_loaded; j++)
            if (!strcmp(patch_name(loaded[j]), assigns[i].name)) p = loaded[j];
        if (!p) {
            p = patch_load(assigns[i].name, perr, sizeof(perr));
            if (!p) {
                fprintf(stderr, "o: patch %s: %s\n", assigns[i].name, perr);
                return 1;
            }
            loaded[n_loaded++] = p;
        }
        if (assigns[i].ch < 0)
            def = patch_as_synth_patch(p);
        else {
            by_chan[assigns[i].ch] = patch_as_synth_patch(p);
            has_chan[assigns[i].ch] = 1;
        }
    }

    Live L = {0};
    L.hold = hold;
    L.s = synth_new(sample_rate, def);
    synth_set_pool_enabled(L.s, pool);
    for (int ch = 0; ch < 16; ch++)
        if (has_chan[ch]) synth_set_channel_patch(L.s, (uint8_t)ch, by_chan[ch]);

    if (selftest) {
        // Diagnostic: drive a note through the exact live render path
        // (ring -> render() -> engine) into a capture buffer, report the
        // peak to prove sound is produced, then play it live for audibility.
        // Done before opening the device so there is a single ring consumer.
        const int frames = 256;
        float chunk[256 * 2];
        int total = sample_rate; // ~1s
        double peak = 0;
        ring_push(&L.ring, (Cmd){0, 0, 60, 100}); // note-on, ch0, note60
        for (int done = 0; done < total; done += frames) {
            render(chunk, frames, &L);
            for (int i = 0; i < frames; i++) {
                double v = chunk[2 * i] < 0 ? -chunk[2 * i] : chunk[2 * i];
                if (v > peak) peak = v;
            }
        }
        printf("selftest: live render path peak = %.4f (1.0 = full scale)\n",
               peak);

        char serr[256], slabel[256];
        AudioOut *sa = audio_open(out_sub, sample_rate, buffer, render, &L,
                                  slabel, sizeof(slabel), serr, sizeof(serr));
        if (sa) {
            printf("selftest: playing note 60 for ~1.2s on %s\n", slabel);
            ring_push(&L.ring, (Cmd){0, 0, 60, 100});
            msleep(1000);
            ring_push(&L.ring, (Cmd){1, 0, 60, 0});
            msleep(400);
            audio_close(sa);
        }
        synth_free(L.s);
        for (int i = 0; i < n_loaded; i++) patch_free(loaded[i]);
        return peak > 0 ? 0 : 1;
    }

    char alabel[256], aerr[256];
    AudioOut *audio = audio_open(out_sub, sample_rate, buffer, render, &L,
                                 alabel, sizeof(alabel), aerr, sizeof(aerr));
    if (!audio) {
        fprintf(stderr, "o: audio: %s\n", aerr);
        return 1;
    }

    double period_ms = (double)buffer / sample_rate * 1000.0;
    printf("audio: %s\n", alabel);
    printf("       %d Hz, %d-frame period (~%.1f ms)\n", sample_rate, buffer,
           period_ms);

    if (!in_sub) {
        fprintf(stderr,
                "o: no MIDI input requested - pass -in <substr> (or -list)\n");
        audio_close(audio);
        return 2;
    }

    char mlabel[256], merr[256];
    MidiIn *midi = midi_open(in_sub, on_midi, &L, mlabel, sizeof(mlabel), merr,
                             sizeof(merr));
    if (!midi) {
        fprintf(stderr, "o: midi: %s\n", merr);
        audio_close(audio);
        return 1;
    }
    printf("midi:  %s\n", mlabel);
    if (hold) printf("hold:  note-offs suppressed\n");
    printf("listening - Ctrl-C to stop\n");

    signal(SIGINT, on_sigint);
    signal(SIGTERM, on_sigint);
    while (!g_stop) msleep(50);

    unsigned long dropped = atomic_load(&L.ring.dropped);
    if (dropped)
        printf("\n(dropped %lu MIDI events under load - try a larger "
               "-buffer-size)\n",
               dropped);

    midi_close(midi);
    audio_close(audio);
    synth_free(L.s);
    for (int i = 0; i < n_loaded; i++) patch_free(loaded[i]);
    return 0;
}
