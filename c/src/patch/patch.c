#include "ondes/patch.h"

#include <ctype.h>
#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

#include "ondes/component.h"
#include "ondes/spec.h"

// Defined in yaml_spec.c.
Spec *yaml_parse(Arena *a, char *mutable_buf, char *err, size_t errlen);

struct Patch {
    char *name;
    Arena arena; // owns the spec tree and the name
    Spec *root;  // top-level map: name -> component spec
    char **order; // sorted component names for deterministic wiring
    size_t n_order;
};

const char *patch_name(const Patch *p) { return p->name; }

// --- path resolution (mirrors the Go loader) ---

static void collect_dir(const char *root, char ***out, size_t *n, size_t *cap) {
    DIR *d = opendir(root);
    if (!d) return;
    struct dirent *e;
    while ((e = readdir(d))) {
        if (e->d_name[0] == '.') continue;
        char path[1024];
        snprintf(path, sizeof(path), "%s/%s", root, e->d_name);
        struct stat st;
        if (stat(path, &st) != 0) continue;
        if (S_ISDIR(st.st_mode)) {
            collect_dir(path, out, n, cap);
        } else {
            size_t len = strlen(path);
            if (len > 5 && strcmp(path + len - 5, ".yaml") == 0) {
                if (*n == *cap) {
                    *cap = *cap ? *cap * 2 : 64;
                    *out = realloc(*out, *cap * sizeof(char *));
                }
                (*out)[(*n)++] = strdup(path);
            }
        }
    }
    closedir(d);
}

static int cmp_str(const void *a, const void *b) {
    return strcmp(*(const char *const *)a, *(const char *const *)b);
}

static char *resolve_path(const char *name) {
    char **paths = NULL;
    size_t n = 0, cap = 0;
    collect_dir("program", &paths, &n, &cap);
    collect_dir("src/main/resources/program", &paths, &n, &cap);
    qsort(paths, n, sizeof(char *), cmp_str);

    char *result = NULL;
    // Exact basename match wins.
    char target[512];
    snprintf(target, sizeof(target), "%s.yaml", name);
    for (size_t i = 0; i < n && !result; i++) {
        const char *base = strrchr(paths[i], '/');
        base = base ? base + 1 : paths[i];
        if (strcmp(base, target) == 0) result = strdup(paths[i]);
    }
    // Otherwise first case-insensitive substring match.
    if (!result) {
        char lname[512];
        size_t j = 0;
        for (; name[j] && j < sizeof(lname) - 1; j++)
            lname[j] = (char)tolower((unsigned char)name[j]);
        lname[j] = 0;
        for (size_t i = 0; i < n && !result; i++) {
            char low[1024];
            size_t k = 0;
            for (; paths[i][k] && k < sizeof(low) - 1; k++)
                low[k] = (char)tolower((unsigned char)paths[i][k]);
            low[k] = 0;
            size_t plen = strlen(paths[i]);
            if (strstr(low, lname) && plen > 5 &&
                strcmp(paths[i] + plen - 5, ".yaml") == 0)
                result = strdup(paths[i]);
        }
    }
    for (size_t i = 0; i < n; i++) free(paths[i]);
    free(paths);
    return result;
}

// --- load ---

Patch *patch_load(const char *name, char *err, size_t errlen) {
    char *path = resolve_path(name);
    if (!path) {
        if (err) snprintf(err, errlen, "patch %s not found", name);
        return NULL;
    }
    FILE *f = fopen(path, "rb");
    if (!f) {
        if (err) snprintf(err, errlen, "cannot open %s", path);
        free(path);
        return NULL;
    }
    fseek(f, 0, SEEK_END);
    long sz = ftell(f);
    fseek(f, 0, SEEK_SET);
    char *buf = malloc((size_t)sz + 1);
    size_t rd = fread(buf, 1, (size_t)sz, f);
    buf[rd] = 0;
    fclose(f);
    free(path);

    Patch *p = calloc(1, sizeof(*p));
    arena_init(&p->arena);
    p->name = arena_strdup(&p->arena, name);
    p->root = yaml_parse(&p->arena, buf, err, errlen);
    free(buf);
    if (!p->root) {
        arena_free(&p->arena);
        free(p);
        return NULL;
    }

    // Build the sorted component-name order.
    p->n_order = p->root->n_entries;
    p->order = arena_alloc(&p->arena, p->n_order * sizeof(char *));
    for (size_t i = 0; i < p->n_order; i++)
        p->order[i] = (char *)p->root->entries[i].key;
    qsort(p->order, p->n_order, sizeof(char *), cmp_str);
    return p;
}

void patch_free(Patch *p) {
    if (!p) return;
    arena_free(&p->arena);
    free(p);
}

// --- apply ---

static const Spec *spec_for(const Patch *p, const char *name) {
    return spec_get(p->root, name);
}

// wire_out connects src to the destination named by `dest`.
static int wire_out(const char *patch_name, const char *src_name,
                    const char *dest, Wire *src, Voice *v) {
    if (!src) return 0;
    if (strcmp(dest, "main") == 0) {
        voice_add_voice_mix_input(v, src);
        return 0;
    }
    char target[256];
    const char *sel = "main";
    const char *dot = strchr(dest, '.');
    if (dot) {
        size_t tl = (size_t)(dot - dest);
        if (tl >= sizeof(target)) tl = sizeof(target) - 1;
        memcpy(target, dest, tl);
        target[tl] = 0;
        sel = dot + 1;
    } else {
        snprintf(target, sizeof(target), "%s", dest);
    }
    Component *dst = voice_component(v, target);
    if (!dst) {
        fprintf(stderr, "patch %s: component %s out: target %s not found\n",
                patch_name, src_name, target);
        return -1;
    }
    if (!component_add_input(dst, sel, src)) {
        fprintf(stderr,
                "patch %s: component %s out: target %s does not accept inputs\n",
                patch_name, src_name, target);
        return -1;
    }
    return 0;
}

// nested_out returns the `out:` field of a nested block (linear-out etc).
static const char *nested_out(const Spec *spec, const char *key) {
    const Spec *m = spec_get(spec, key);
    if (!spec_is_map(m)) return NULL;
    return spec_get_str(m, "out");
}

static int patch_apply(void *ctx, Voice *v) {
    Patch *p = ctx;
    Arena *a = voice_arena(v);

    // Pass 1: make + configure every component.
    for (size_t i = 0; i < p->n_order; i++) {
        const char *name = p->order[i];
        const Spec *spec = spec_for(p, name);
        const char *type = spec_get_str(spec, "type");
        if (!type) {
            fprintf(stderr, "patch %s: component %s missing type\n", p->name,
                    name);
            return -1;
        }
        Component *c = component_make(a, type);
        if (!c) {
            fprintf(stderr, "patch %s: component %s: unknown type %s\n", p->name,
                    name, type);
            return -1;
        }
        if (component_configure(c, spec, v, name) != 0) {
            fprintf(stderr, "patch %s: component %s: configure failed\n",
                    p->name, name);
            return -1;
        }
        voice_add_component(v, name, c);
    }

    // Pass 2: resolve out: / linear-out: / log-out: / out-level:.
    for (size_t i = 0; i < p->n_order; i++) {
        const char *name = p->order[i];
        const Spec *spec = spec_for(p, name);
        Component *c = voice_component(v, name);

        const char *dest = spec_get_str(spec, "out");
        if (dest && wire_out(p->name, name, dest, component_output(c), v) != 0)
            return -1;

        const char *keys[] = {"linear-out", "log-out"};
        for (int k = 0; k < 2; k++) {
            const char *nd = nested_out(spec, keys[k]);
            if (!nd) continue;
            Wire *w = component_named_output(c, keys[k]);
            if (w && wire_out(p->name, name, nd, w, v) != 0) return -1;
        }
        const char *ol = spec_get_str(spec, "out-level");
        if (ol) {
            Wire *w = component_named_output(c, "out-level");
            if (w && wire_out(p->name, name, ol, w, v) != 0) return -1;
        }
    }
    return 0;
}

SynthPatch patch_as_synth_patch(Patch *p) {
    SynthPatch sp = {.ctx = p, .apply = patch_apply};
    return sp;
}
