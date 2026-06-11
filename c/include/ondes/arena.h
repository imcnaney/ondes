// Arena (region) allocator. A Voice owns one arena; every component,
// wire, and per-voice buffer allocates from it, so tearing the voice down
// is a single arena_free rather than a walk of individually-malloc'd
// objects. This keeps voice teardown O(1) and the per-sample path free of
// the fragmentation a churn of malloc/free would cause.
#ifndef ONDES_ARENA_H
#define ONDES_ARENA_H

#include <stddef.h>

typedef struct ArenaBlock ArenaBlock;

typedef struct Arena {
    ArenaBlock *head; // most-recent block; blocks chain toward older ones
} Arena;

// arena_init zeroes an arena (no allocation yet).
void arena_init(Arena *a);

// arena_alloc returns zeroed, max-aligned storage of n bytes from the
// arena, growing it with a fresh block when the current one is full.
// Returns NULL only on out-of-memory.
void *arena_alloc(Arena *a, size_t n);

// arena_strdup copies a NUL-terminated string into the arena.
char *arena_strdup(Arena *a, const char *s);

// arena_grow returns a fresh array of new_n*elem bytes with the first
// old_n*elem bytes copied from old. The old block is not reclaimed until
// arena_free, so this is only for the append-once growth of small
// per-voice vectors (wire lists, junction inputs) - total overhead is ~2x
// for geometric growth, all released at teardown. old may be NULL.
void *arena_grow(Arena *a, void *old, size_t old_n, size_t new_n, size_t elem);

// arena_free releases every block. The arena is reusable afterward.
void arena_free(Arena *a);

// ArenaSnapshot captures the live contents of every block so the arena can
// later be restored byte-for-byte. It underpins voice recycling: after a
// voice graph is built, snapshot it; to reuse the voice for a new note,
// restore it - resetting ALL in-arena component state to its exact
// post-construction values with no risk of missing a field. Because the
// blocks are not moved, every pointer in the restored data stays valid.
//
// Valid only while the arena is not grown after the snapshot (the synth's
// per-sample path never allocates, so this holds during play).
typedef struct ArenaSnapshot ArenaSnapshot;

ArenaSnapshot *arena_snapshot(Arena *a);
void arena_restore(Arena *a, const ArenaSnapshot *snap);
void arena_snapshot_free(ArenaSnapshot *snap);

#endif // ONDES_ARENA_H
