#include "ondes/limiter.h"

#include <math.h>
#include <stdlib.h>

// NewLimiter mirrors src/main/resources/config/main-limiter-config.yaml:
//   max-in    = 0x7fffffff   (full int32)
//   max-out   = 0x7fff       (int16 max)
//   threshold = 0x6fff       (256 below max-out)
//   delay-ms  = 200          (sliding max window)
// Values are scaled by 1/32767 so that 1.0 == int16 full-scale.
Limiter *limiter_new(int sample_rate) {
    const double scale = 32767.0;
    Limiter *l = calloc(1, sizeof(*l));
    l->max_in = (double)0x7fffffff / scale;
    l->max_out = (double)0x7fff / scale;
    l->threshold = (double)0x6fff / scale;
    l->slope = (l->max_out - l->threshold) / (l->max_in - l->threshold);
    l->window = sample_rate / 5; // 200 ms
    l->bypass = true;
    l->cap = (size_t)l->window + 2;
    l->deque = malloc(l->cap * sizeof(*l->deque));
    return l;
}

void limiter_free(Limiter *l) {
    if (!l) return;
    free(l->deque);
    free(l);
}

static void deque_clear(Limiter *l) { l->head = l->tail = l->count = 0; }

static LimiterSample deque_back(const Limiter *l) {
    return l->deque[(l->tail + l->cap - 1) % l->cap];
}

static void deque_pop_back(Limiter *l) {
    l->tail = (l->tail + l->cap - 1) % l->cap;
    l->count--;
}

static void deque_push_back(Limiter *l, LimiterSample s) {
    l->deque[l->tail] = s;
    l->tail = (l->tail + 1) % l->cap;
    l->count++;
}

static LimiterSample deque_front(const Limiter *l) { return l->deque[l->head]; }

static void deque_pop_front(Limiter *l) {
    l->head = (l->head + 1) % l->cap;
    l->count--;
}

// limiter_apply is faithful to the Java/Go Limiter, including the
// asymmetric cold-start: while bypassing, only positive samples above
// threshold wake the tracker; once active, both polarities are tracked.
double limiter_apply(Limiter *l, double sum) {
    l->idx++;
    if (l->bypass && sum < l->threshold) return sum;

    double abs = sum < 0 ? -sum : sum;

    while (l->count > 0 && deque_back(l).abs <= abs) deque_pop_back(l);
    LimiterSample push = {l->idx, abs};
    deque_push_back(l, push);

    int64_t cutoff = l->idx - (int64_t)l->window;
    while (l->count > 0 && deque_front(l).idx <= cutoff) deque_pop_front(l);

    if (l->count == 0) return sum;
    double max = deque_front(l).abs;
    if (max < l->threshold) {
        l->bypass = true;
        deque_clear(l);
        return sum;
    }
    l->bypass = false;
    double adjusted = l->slope * (max - l->threshold) + l->threshold;
    return (adjusted / max) * sum;
}
