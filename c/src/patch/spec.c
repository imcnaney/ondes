#include "ondes/spec.h"

#include <stdlib.h>
#include <string.h>

const Spec *spec_get(const Spec *map, const char *key) {
    if (!spec_is_map(map)) return NULL;
    for (size_t i = 0; i < map->n_entries; i++)
        if (strcmp(map->entries[i].key, key) == 0) return map->entries[i].val;
    return NULL;
}

const char *spec_get_str(const Spec *map, const char *key) {
    const Spec *s = spec_get(map, key);
    return spec_is_scalar(s) ? s->scalar : NULL;
}

const char *spec_str(const Spec *map, const char *key, const char *def) {
    const char *s = spec_get_str(map, key);
    return s ? s : def;
}

double spec_double(const Spec *map, const char *key, double def) {
    const char *s = spec_get_str(map, key);
    if (!s || !*s) return def;
    char *end;
    double v = strtod(s, &end);
    return end == s ? def : v;
}

long spec_int(const Spec *map, const char *key, long def) {
    const char *s = spec_get_str(map, key);
    if (!s || !*s) return def;
    char *end;
    long v = strtol(s, &end, 10);
    return end == s ? def : v;
}

bool spec_bool(const Spec *map, const char *key, bool def) {
    const char *s = spec_get_str(map, key);
    if (!s) return def;
    // YAML truthy spellings used in the patch corpus.
    if (!strcmp(s, "true") || !strcmp(s, "yes") || !strcmp(s, "on") ||
        !strcmp(s, "True") || !strcmp(s, "1"))
        return true;
    if (!strcmp(s, "false") || !strcmp(s, "no") || !strcmp(s, "off") ||
        !strcmp(s, "False") || !strcmp(s, "0"))
        return false;
    return def;
}
