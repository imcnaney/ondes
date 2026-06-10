#include "ondes/wav.h"

#include <stdint.h>
#include <stdio.h>

#define BITS_PER_SAMPLE 16
#define NUM_CHANNELS 1

static void put_u32(FILE *f, uint32_t v) {
    uint8_t b[4] = {(uint8_t)v, (uint8_t)(v >> 8), (uint8_t)(v >> 16),
                    (uint8_t)(v >> 24)};
    fwrite(b, 1, 4, f);
}

static void put_u16(FILE *f, uint16_t v) {
    uint8_t b[2] = {(uint8_t)v, (uint8_t)(v >> 8)};
    fwrite(b, 1, 2, f);
}

int wav_write_mono16(const char *path, const double *samples, size_t n,
                     int sample_rate) {
    FILE *f = fopen(path, "wb");
    if (!f) return -1;

    uint32_t byte_rate = (uint32_t)sample_rate * NUM_CHANNELS * BITS_PER_SAMPLE / 8;
    uint16_t block_align = NUM_CHANNELS * BITS_PER_SAMPLE / 8;
    uint32_t data_size = (uint32_t)(n * NUM_CHANNELS * BITS_PER_SAMPLE / 8);

    fwrite("RIFF", 1, 4, f);
    put_u32(f, 36 + data_size);
    fwrite("WAVE", 1, 4, f);

    fwrite("fmt ", 1, 4, f);
    put_u32(f, 16);
    put_u16(f, 1); // PCM
    put_u16(f, NUM_CHANNELS);
    put_u32(f, (uint32_t)sample_rate);
    put_u32(f, byte_rate);
    put_u16(f, block_align);
    put_u16(f, BITS_PER_SAMPLE);

    fwrite("data", 1, 4, f);
    put_u32(f, data_size);

    for (size_t i = 0; i < n; i++) {
        int32_t v = (int32_t)(samples[i] * 32767);
        if (v > 32767)
            v = 32767;
        else if (v < -32768)
            v = -32768;
        put_u16(f, (uint16_t)(int16_t)v);
    }
    int ok = !ferror(f);
    fclose(f);
    return ok ? 0 : -1;
}
