// Standard MIDI File parsing. Mirrors the Go port's midi/smf.go: collect
// note-on/off, control-change and tempo events from every track, merge
// stably by absolute tick, then walk in tick order converting to sample
// positions with the running tempo (default 120 BPM).
#include "ondes/smf.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    uint64_t tick;
    uint32_t seq; // insertion order, for a stable sort within a tick
    bool tempo;
    uint32_t tempo_us; // microseconds per quarter (when tempo)
    MidiMsg msg;       // channel message (when !tempo)
} TickEv;

typedef struct {
    const uint8_t *p;
    const uint8_t *end;
} Reader;

static uint32_t read_varlen(Reader *r) {
    uint32_t v = 0;
    while (r->p < r->end) {
        uint8_t b = *r->p++;
        v = (v << 7) | (b & 0x7F);
        if (!(b & 0x80)) break;
    }
    return v;
}

static uint32_t read_be(const uint8_t *p, int n) {
    uint32_t v = 0;
    for (int i = 0; i < n; i++) v = (v << 8) | p[i];
    return v;
}

typedef struct {
    TickEv *a;
    size_t n, cap;
} EvVec;

static void ev_push(EvVec *v, TickEv e) {
    if (v->n == v->cap) {
        v->cap = v->cap ? v->cap * 2 : 256;
        v->a = realloc(v->a, v->cap * sizeof(TickEv));
    }
    v->a[v->n++] = e;
}

// parse_track reads one MTrk body [p,end) into v, using *seq for stable
// ordering. Returns false on a malformed event stream.
static bool parse_track(EvVec *v, const uint8_t *p, const uint8_t *end,
                        uint32_t *seq) {
    Reader r = {p, end};
    uint64_t abs = 0;
    uint8_t status = 0;
    while (r.p < r.end) {
        abs += read_varlen(&r);
        if (r.p >= r.end) break;
        uint8_t b = *r.p;
        if (b & 0x80) {
            status = b;
            r.p++;
        }
        // else running status: reuse previous status, b is first data byte.

        if (status == 0xFF) {
            // meta event
            if (r.p >= r.end) return false;
            uint8_t type = *r.p++;
            uint32_t len = read_varlen(&r);
            if (r.p + len > r.end) return false;
            if (type == 0x51 && len == 3) {
                TickEv e = {0};
                e.tick = abs;
                e.seq = (*seq)++;
                e.tempo = true;
                e.tempo_us = read_be(r.p, 3);
                ev_push(v, e);
            }
            r.p += len;
        } else if (status == 0xF0 || status == 0xF7) {
            uint32_t len = read_varlen(&r);
            if (r.p + len > r.end) return false;
            r.p += len;
        } else {
            uint8_t hi = status & 0xF0;
            uint8_t ch = status & 0x0F;
            uint8_t d1 = 0, d2 = 0;
            int nbytes = (hi == 0xC0 || hi == 0xD0) ? 1 : 2;
            if (r.p + nbytes > r.end) return false;
            d1 = *r.p++;
            if (nbytes == 2) d2 = *r.p++;
            if (hi == 0x90 || hi == 0x80 || hi == 0xB0) {
                TickEv e = {0};
                e.tick = abs;
                e.seq = (*seq)++;
                e.msg = (MidiMsg){(uint8_t)(hi | ch), d1, d2};
                ev_push(v, e);
            }
        }
    }
    return true;
}

static int cmp_ev(const void *a, const void *b) {
    const TickEv *x = a, *y = b;
    if (x->tick != y->tick) return x->tick < y->tick ? -1 : 1;
    return x->seq < y->seq ? -1 : (x->seq > y->seq ? 1 : 0);
}

MidiEvent *smf_read(const char *path, int sample_rate, size_t *n_out, char *err,
                    size_t errlen) {
    *n_out = 0;
    FILE *f = fopen(path, "rb");
    if (!f) {
        if (err) snprintf(err, errlen, "cannot open %s", path);
        return NULL;
    }
    fseek(f, 0, SEEK_END);
    long sz = ftell(f);
    fseek(f, 0, SEEK_SET);
    uint8_t *buf = malloc((size_t)sz);
    if (fread(buf, 1, (size_t)sz, f) != (size_t)sz) {
        fclose(f);
        free(buf);
        if (err) snprintf(err, errlen, "short read on %s", path);
        return NULL;
    }
    fclose(f);

    const uint8_t *p = buf;
    const uint8_t *end = buf + sz;
    if (sz < 14 || memcmp(p, "MThd", 4) != 0) {
        free(buf);
        if (err) snprintf(err, errlen, "not an SMF file");
        return NULL;
    }
    uint32_t hlen = read_be(p + 4, 4);
    uint16_t division = (uint16_t)read_be(p + 12, 2);
    p += 8 + hlen;
    if (division & 0x8000) {
        free(buf);
        if (err) snprintf(err, errlen, "SMPTE time format not supported");
        return NULL;
    }
    uint32_t ppq = division;
    if (ppq == 0) ppq = 1;

    EvVec v = {0};
    uint32_t seq = 0;
    while (p + 8 <= end) {
        uint32_t clen = read_be(p + 4, 4);
        if (memcmp(p, "MTrk", 4) != 0) {
            p += 8 + clen; // skip unknown chunk
            continue;
        }
        const uint8_t *tp = p + 8;
        const uint8_t *te = tp + clen;
        if (te > end) te = end;
        if (!parse_track(&v, tp, te, &seq)) {
            free(buf);
            free(v.a);
            if (err) snprintf(err, errlen, "malformed track in %s", path);
            return NULL;
        }
        p += 8 + clen;
    }
    free(buf);

    qsort(v.a, v.n, sizeof(TickEv), cmp_ev);

    MidiEvent *out = malloc((v.n ? v.n : 1) * sizeof(MidiEvent));
    size_t no = 0;
    double bpm = 120.0;
    uint64_t last_tick = 0;
    double cur_sec = 0;
    for (size_t i = 0; i < v.n; i++) {
        TickEv *e = &v.a[i];
        cur_sec += (double)(e->tick - last_tick) * 60.0 / (bpm * (double)ppq);
        last_tick = e->tick;
        if (e->tempo) {
            if (e->tempo_us > 0) bpm = 60000000.0 / (double)e->tempo_us;
            continue;
        }
        out[no].sample = (int64_t)(cur_sec * (double)sample_rate + 0.5);
        out[no].msg = e->msg;
        no++;
    }
    free(v.a);
    *n_out = no;
    return out;
}
