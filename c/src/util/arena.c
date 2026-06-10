#include "ondes/arena.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Default block payload. Voice graphs are small (a few KB even for the
// heaviest fixture, per doc/timings.md), so one block usually suffices;
// an oversized request gets its own exact-fit block.
#define ARENA_BLOCK 8192
#define ARENA_ALIGN (sizeof(max_align_t))

struct ArenaBlock {
    ArenaBlock *next;
    size_t used;
    size_t cap;
    // payload follows immediately, max-aligned because the block itself
    // is malloc'd (malloc returns suitably-aligned storage) and the
    // header size is padded up to the alignment below.
    unsigned char data[];
};

void arena_init(Arena *a) { a->head = NULL; }

static size_t align_up(size_t n) {
    return (n + (ARENA_ALIGN - 1)) & ~(ARENA_ALIGN - 1);
}

static ArenaBlock *new_block(size_t payload) {
    if (payload < ARENA_BLOCK) payload = ARENA_BLOCK;
    ArenaBlock *b = malloc(sizeof(ArenaBlock) + payload);
    if (!b) return NULL;
    b->next = NULL;
    b->used = 0;
    b->cap = payload;
    return b;
}

void *arena_alloc(Arena *a, size_t n) {
    n = align_up(n);
    if (!a->head || a->head->used + n > a->head->cap) {
        ArenaBlock *b = new_block(n);
        if (!b) return NULL;
        b->next = a->head;
        a->head = b;
    }
    void *p = a->head->data + a->head->used;
    a->head->used += n;
    memset(p, 0, n);
    return p;
}

char *arena_strdup(Arena *a, const char *s) {
    size_t len = strlen(s) + 1;
    char *p = arena_alloc(a, len);
    if (p) memcpy(p, s, len);
    return p;
}

void *arena_grow(Arena *a, void *old, size_t old_n, size_t new_n,
                 size_t elem) {
    void *p = arena_alloc(a, new_n * elem);
    if (p && old && old_n) memcpy(p, old, old_n * elem);
    return p;
}

void arena_free(Arena *a) {
    ArenaBlock *b = a->head;
    while (b) {
        ArenaBlock *next = b->next;
        free(b);
        b = next;
    }
    a->head = NULL;
}

struct ArenaSnapshot {
    size_t n;
    struct {
        ArenaBlock *blk;
        size_t used;
        void *copy;
    } *items;
};

ArenaSnapshot *arena_snapshot(Arena *a) {
    size_t n = 0;
    for (ArenaBlock *b = a->head; b; b = b->next) n++;
    ArenaSnapshot *s = malloc(sizeof(*s));
    s->n = n;
    s->items = malloc(n * sizeof(*s->items));
    size_t i = 0;
    for (ArenaBlock *b = a->head; b; b = b->next, i++) {
        s->items[i].blk = b;
        s->items[i].used = b->used;
        s->items[i].copy = malloc(b->used);
        memcpy(s->items[i].copy, b->data, b->used);
    }
    return s;
}

void arena_restore(Arena *a, const ArenaSnapshot *snap) {
    (void)a; // blocks are addressed directly; no growth allowed since snapshot
    for (size_t i = 0; i < snap->n; i++) {
        memcpy(snap->items[i].blk->data, snap->items[i].copy,
               snap->items[i].used);
        snap->items[i].blk->used = snap->items[i].used;
    }
}

void arena_snapshot_free(ArenaSnapshot *snap) {
    if (!snap) return;
    for (size_t i = 0; i < snap->n; i++) free(snap->items[i].copy);
    free(snap->items);
    free(snap);
}
