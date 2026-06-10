// A focused block-style YAML parser for the OndeSynth patch subset:
// block mappings (top-level and nested), block sequences whose items are
// plain (possibly multi-token) scalars or empty, scalar values, full-line
// and inline `#` comments, and blank lines. The corpus uses no flow
// collections, quotes, anchors/aliases, or block scalars (verified across
// all 109 patch files), so those are intentionally unsupported. Produces
// the Spec tree consumed by the components.
#include <ctype.h>
#include <stdio.h>
#include <string.h>

#include "ondes/arena.h"
#include "ondes/spec.h"

typedef struct {
    int indent;
    char *text; // comment-stripped, right-trimmed; never empty
} Line;

typedef struct {
    Arena *a;
    Line *lines;
    size_t n;
    size_t pos;
} Parser;

static Spec *new_spec(Arena *a, SpecKind k) {
    Spec *s = arena_alloc(a, sizeof(*s));
    s->kind = k;
    return s;
}

static Spec *scalar_spec(Arena *a, const char *text) {
    Spec *s = new_spec(a, SPEC_SCALAR);
    s->scalar = arena_strdup(a, text);
    return s;
}

// strip_comment cuts the line at an unquoted `#` that starts the line or is
// preceded by whitespace, then right-trims. Returns the indent (leading
// space/tab count) and writes the trimmed body via *body (NULL if blank).
static int strip_comment(char *raw, char **body) {
    int indent = 0;
    char *p = raw;
    while (*p == ' ' || *p == '\t') {
        indent++;
        p++;
    }
    // Find comment start.
    char *start = p;
    char *q = p;
    char prev = ' ';
    while (*q) {
        if (*q == '#' && (q == start || prev == ' ' || prev == '\t')) break;
        prev = *q;
        q++;
    }
    *q = 0;
    // right-trim
    while (q > p && (q[-1] == ' ' || q[-1] == '\t' || q[-1] == '\r')) *--q = 0;
    *body = (*p) ? p : NULL;
    return indent;
}

// split_lines tokenizes the buffer into non-empty logical lines.
static void split_lines(Parser *ps, char *buf) {
    size_t cap = 0;
    char *line = buf;
    while (*line || line == buf) {
        char *nl = strchr(line, '\n');
        if (nl) *nl = 0;
        char *body;
        int indent = strip_comment(line, &body);
        if (body) {
            if (ps->n == cap) {
                cap = cap ? cap * 2 : 64;
                ps->lines =
                    arena_grow(ps->a, ps->lines, ps->n, cap, sizeof(Line));
            }
            ps->lines[ps->n].indent = indent;
            ps->lines[ps->n].text = body;
            ps->n++;
        }
        if (!nl) break;
        line = nl + 1;
    }
}

static char *trim(char *s) {
    while (*s == ' ' || *s == '\t') s++;
    char *e = s + strlen(s);
    while (e > s && (e[-1] == ' ' || e[-1] == '\t' || e[-1] == '\r')) *--e = 0;
    return s;
}

static Spec *parse_block(Parser *ps, int min_indent);

// parse_seq consumes consecutive '-' lines at exactly `indent`.
static Spec *parse_seq(Parser *ps, int indent) {
    Spec *seq = new_spec(ps->a, SPEC_SEQ);
    size_t cap = 0;
    while (ps->pos < ps->n && ps->lines[ps->pos].indent == indent &&
           ps->lines[ps->pos].text[0] == '-') {
        char *content = ps->lines[ps->pos].text + 1; // after '-'
        content = trim(content);
        ps->pos++;
        Spec *item;
        if (*content) {
            item = scalar_spec(ps->a, content);
        } else if (ps->pos < ps->n && ps->lines[ps->pos].indent > indent) {
            item = parse_block(ps, indent + 1);
        } else {
            item = NULL; // empty item (e.g. a bare `-`)
        }
        if (seq->n_items == cap) {
            cap = cap ? cap * 2 : 8;
            seq->items =
                arena_grow(ps->a, seq->items, seq->n_items, cap, sizeof(Spec *));
        }
        seq->items[seq->n_items++] = item;
    }
    return seq;
}

// parse_map consumes consecutive "key: value" lines at exactly `indent`.
static Spec *parse_map(Parser *ps, int indent) {
    Spec *map = new_spec(ps->a, SPEC_MAP);
    size_t cap = 0;
    while (ps->pos < ps->n && ps->lines[ps->pos].indent == indent &&
           ps->lines[ps->pos].text[0] != '-') {
        char *text = ps->lines[ps->pos].text;
        char *colon = strchr(text, ':');
        if (!colon) {
            ps->pos++; // skip malformed line
            continue;
        }
        *colon = 0;
        char *key = trim(text);
        char *rest = trim(colon + 1);
        ps->pos++;

        Spec *val;
        if (*rest) {
            val = scalar_spec(ps->a, rest);
        } else if (ps->pos < ps->n && ps->lines[ps->pos].indent > indent) {
            // Nested block (map or sequence) indented under the key.
            val = parse_block(ps, indent + 1);
        } else if (ps->pos < ps->n && ps->lines[ps->pos].indent == indent &&
                   ps->lines[ps->pos].text[0] == '-') {
            // Block sequence whose `-` items align with the key (common
            // YAML style, e.g. `points:` then `- ...` at the same indent).
            val = parse_seq(ps, indent);
        } else {
            val = scalar_spec(ps->a, "");
        }

        if (map->n_entries == cap) {
            cap = cap ? cap * 2 : 8;
            map->entries = arena_grow(ps->a, map->entries, map->n_entries, cap,
                                      sizeof(SpecEntry));
        }
        map->entries[map->n_entries].key = arena_strdup(ps->a, key);
        map->entries[map->n_entries].val = val;
        map->n_entries++;
    }
    return map;
}

// parse_block dispatches to seq or map based on the first line's content,
// using that line's actual indent (which must be >= min_indent).
static Spec *parse_block(Parser *ps, int min_indent) {
    int indent = ps->lines[ps->pos].indent;
    (void)min_indent;
    if (ps->lines[ps->pos].text[0] == '-') return parse_seq(ps, indent);
    return parse_map(ps, indent);
}

Spec *yaml_parse(Arena *a, char *mutable_buf, char *err, size_t errlen) {
    Parser ps = {0};
    ps.a = a;
    split_lines(&ps, mutable_buf);
    if (ps.n == 0) {
        if (err) snprintf(err, errlen, "empty document");
        return NULL;
    }
    Spec *root = parse_block(&ps, 0);
    if (!spec_is_map(root)) {
        if (err) snprintf(err, errlen, "top level is not a mapping");
        return NULL;
    }
    return root;
}
