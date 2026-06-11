// Mono 16-bit PCM WAV output for the renderer, matching what
// regression/diff_summaries.py expects.
#ifndef ONDES_WAV_H
#define ONDES_WAV_H

#include <stddef.h>

#define ONDES_SAMPLE_RATE 44100

// wav_write_mono16 writes float64 samples in [-1, +1] as a mono 16-bit PCM
// WAV (out-of-range values hard-clipped). Returns 0 on success.
int wav_write_mono16(const char *path, const double *samples, size_t n,
                     int sample_rate);

#endif // ONDES_WAV_H
