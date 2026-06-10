#include "computil.h"

#include <stdlib.h>
#include <string.h>

static void push_d(Arena *a, double **arr, size_t *n, size_t *cap, double v) {
    if (*n == *cap) {
        size_t c = *cap ? *cap * 2 : 8;
        *arr = arena_grow(a, *arr, *n, c, sizeof(**arr));
        *cap = c;
    }
    (*arr)[(*n)++] = v;
}

static bool is_sep(char c) {
    return c == ' ' || c == '\t' || c == ',' || c == '\n' || c == '\r';
}

// tokenize_into parses one scalar string's numeric tokens, appending each
// to arr. Sets *ok=false on any unparseable token.
static void tokenize_into(Arena *a, const char *s, double **arr, size_t *n,
                          size_t *cap, bool *ok) {
    if (!s) return;
    while (*s) {
        while (*s && is_sep(*s)) s++;
        if (!*s) break;
        char *end;
        double v = strtod(s, &end);
        if (end == s) {
            if (ok) *ok = false;
            // skip the bad token
            while (*s && !is_sep(*s)) s++;
            continue;
        }
        push_d(a, arr, n, cap, v);
        s = end;
    }
}

double *spec_tokens_doubles(Arena *a, const Spec *s, size_t *n, bool *ok) {
    *n = 0;
    if (ok) *ok = true;
    if (!s) {
        if (ok) *ok = false;
        return NULL;
    }
    double *arr = NULL;
    size_t cap = 0;
    if (s->kind == SPEC_SCALAR) {
        tokenize_into(a, s->scalar, &arr, n, &cap, ok);
    } else if (s->kind == SPEC_SEQ) {
        for (size_t i = 0; i < s->n_items; i++) {
            const Spec *it = s->items[i];
            if (!it) continue;
            if (it->kind == SPEC_SCALAR) {
                tokenize_into(a, it->scalar, &arr, n, &cap, ok);
            } else if (it->kind == SPEC_SEQ) {
                for (size_t j = 0; j < it->n_items; j++)
                    if (it->items[j] && it->items[j]->kind == SPEC_SCALAR)
                        tokenize_into(a, it->items[j]->scalar, &arr, n, &cap,
                                      ok);
            }
        }
    } else {
        if (ok) *ok = false;
    }
    return arr;
}
