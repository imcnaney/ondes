// p renders a MIDI file through a synth patch into a WAV file, mirroring
// the Go cmd/p and the regression render loop. Usage:
//   p [-patch name] [-tail sec] [-max-tail sec] <in.mid> <out.wav>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "ondes/component.h"
#include "ondes/patch.h"
#include "ondes/smf.h"
#include "ondes/synth.h"
#include "ondes/wav.h"

int main(int argc, char **argv) {
    const char *patch_name = "sine";
    double tail_sec = 0.0023; // ~100 samples, Java default tail
    double max_tail_sec = 30.0;
    int pool = 0;

    int i = 1;
    for (; i < argc; i++) {
        if (!strcmp(argv[i], "-patch") && i + 1 < argc)
            patch_name = argv[++i];
        else if (!strcmp(argv[i], "-tail") && i + 1 < argc)
            tail_sec = atof(argv[++i]);
        else if (!strcmp(argv[i], "-max-tail") && i + 1 < argc)
            max_tail_sec = atof(argv[++i]);
        else if (!strcmp(argv[i], "-pool"))
            pool = 1;
        else if (argv[i][0] != '-')
            break;
    }
    if (argc - i != 2) {
        fprintf(stderr,
                "usage: %s [-patch name] [-tail sec] <in.mid> <out.wav>\n",
                argv[0]);
        return 2;
    }
    const char *in_mid = argv[i];
    const char *out_wav = argv[i + 1];

    component_register_all();

    char err[256];
    Patch *p = patch_load(patch_name, err, sizeof(err));
    if (!p) {
        fprintf(stderr, "patch: %s\n", err);
        return 1;
    }

    size_t nev;
    MidiEvent *events = smf_read(in_mid, ONDES_SAMPLE_RATE, &nev, err, sizeof(err));
    if (!events) {
        fprintf(stderr, "midi: %s\n", err);
        return 1;
    }
    if (nev == 0) {
        fprintf(stderr, "midi: %s contains no note events\n", in_mid);
        return 1;
    }

    int64_t last = events[nev - 1].sample;
    int64_t min_end = last + (int64_t)(tail_sec * ONDES_SAMPLE_RATE);
    int64_t max_end = last + (int64_t)(max_tail_sec * ONDES_SAMPLE_RATE);

    Synth *s = synth_new(ONDES_SAMPLE_RATE, patch_as_synth_patch(p));
    synth_set_pool_enabled(s, pool);

    size_t cap = (size_t)min_end + 1;
    double *samples = malloc(cap * sizeof(double));
    size_t ns = 0;

    size_t ei = 0;
    for (int64_t n = 0;; n++) {
        while (ei < nev && events[ei].sample <= n) {
            MidiMsg m = events[ei].msg;
            uint8_t ch = m.status & 0x0F;
            if (midi_is_note_on(m))
                synth_note_on(s, ch, m.data1, m.data2);
            else if (midi_is_note_off(m))
                synth_note_off(s, ch, m.data1);
            else if ((m.status & 0xF0) == 0xB0)
                synth_control_change(s, ch, m.data1, m.data2);
            ei++;
        }
        if (ns == cap) {
            cap *= 2;
            samples = realloc(samples, cap * sizeof(double));
        }
        samples[ns++] = synth_step(s);
        if (n >= min_end && ei >= nev && synth_active_voices(s) == 0) break;
        if (n >= max_end) break;
    }

    if (wav_write_mono16(out_wav, samples, ns, ONDES_SAMPLE_RATE) != 0) {
        fprintf(stderr, "wav: write failed\n");
        return 1;
    }
    printf("rendered %zu samples (%.2fs audio) - %s\n", ns,
           (double)ns / ONDES_SAMPLE_RATE, out_wav);

    free(samples);
    free(events);
    synth_free(s);
    patch_free(p);
    return 0;
}
