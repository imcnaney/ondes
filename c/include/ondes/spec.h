// Spec is the in-memory tree a YAML component definition deserializes to,
// mirroring the Go port's component.Spec (map[string]any). Scalars are
// kept as raw strings and parsed on demand, sidestepping YAML's
// int/float/string ambiguity. The whole tree for a patch lives in the
// patch's own arena.
#ifndef ONDES_SPEC_H
#define ONDES_SPEC_H

#include <stdbool.h>
#include <stddef.h>

typedef enum { SPEC_SCALAR, SPEC_SEQ, SPEC_MAP } SpecKind;

typedef struct Spec Spec;
typedef struct {
    const char *key;
    Spec *val;
} SpecEntry;

struct Spec {
    SpecKind kind;
    const char *scalar; // SPEC_SCALAR
    Spec **items;       // SPEC_SEQ
    size_t n_items;
    SpecEntry *entries; // SPEC_MAP
    size_t n_entries;
};

// spec_get returns the child of a map under key, or NULL (also NULL if
// map is not a map).
const Spec *spec_get(const Spec *map, const char *key);

// spec_get_str returns a scalar child's raw string, or NULL.
const char *spec_get_str(const Spec *map, const char *key);

// Typed accessors with defaults. Missing or unparseable keys return def.
const char *spec_str(const Spec *map, const char *key, const char *def);
double spec_double(const Spec *map, const char *key, double def);
long spec_int(const Spec *map, const char *key, long def);
bool spec_bool(const Spec *map, const char *key, bool def);

static inline bool spec_is_scalar(const Spec *s) {
    return s && s->kind == SPEC_SCALAR;
}
static inline bool spec_is_seq(const Spec *s) { return s && s->kind == SPEC_SEQ; }
static inline bool spec_is_map(const Spec *s) { return s && s->kind == SPEC_MAP; }

#endif // ONDES_SPEC_H
