// Limiter is the soft peak limiter applied to the main mix, ported from
// Java's ondes.synth.envelope.Limiter + MaxTrackerPQ (via the Go port).
// Below threshold the signal passes through; above it the signal is
// compressed toward max-out so loud bursts of polyphony are smoothly
// clamped rather than hard-clipped. 1.0 maps to int16 full-scale (32767).
#ifndef ONDES_LIMITER_H
#define ONDES_LIMITER_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef struct {
    int64_t idx;
    double abs;
} LimiterSample;

typedef struct Limiter {
    double max_in, max_out, threshold;
    double slope;
    int window;

    // Monotonic deque of (idx, |value|) with strictly decreasing values;
    // the front is the running max over the lookahead window. Stored in a
    // ring buffer sized to window+1.
    LimiterSample *deque;
    size_t head, tail, count, cap;

    bool bypass;
    int64_t idx;
} Limiter;

Limiter *limiter_new(int sample_rate);
void limiter_free(Limiter *l);

// limiter_apply takes one summed sample and returns the (possibly
// compressed) output.
double limiter_apply(Limiter *l, double sum);

#endif // ONDES_LIMITER_H
