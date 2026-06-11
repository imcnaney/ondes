#include "ondes/component.h"

#include <string.h>

// Built-in component constructors. Each component file defines a factory
// of this shape and is wired in via component_register_all below.
Component *wave_new(Arena *a);
Component *filter_new(Arena *a);
Component *env_new(Arena *a);
Component *mix_new(Arena *a);
Component *dynamic_mix_new(Arena *a);
Component *balancer_new(Arena *a);
Component *opamp_new(Arena *a);
Component *controller_new(Arena *a);
Component *smooth_new(Arena *a);
Component *midinote_new(Arena *a);
Component *echo_new(Arena *a);

#define MAX_TYPES 32

static struct {
    const char *type;
    ComponentFactory f;
} g_registry[MAX_TYPES];
static size_t g_n;

void component_register(const char *type, ComponentFactory f) {
    for (size_t i = 0; i < g_n; i++)
        if (strcmp(g_registry[i].type, type) == 0) return; // idempotent
    if (g_n < MAX_TYPES) {
        g_registry[g_n].type = type;
        g_registry[g_n].f = f;
        g_n++;
    }
}

Component *component_make(Arena *a, const char *type) {
    for (size_t i = 0; i < g_n; i++)
        if (strcmp(g_registry[i].type, type) == 0) return g_registry[i].f(a);
    return NULL;
}

void component_register_all(void) {
    component_register("wave", wave_new);
    component_register("filter", filter_new);
    component_register("env", env_new);
    component_register("mix", mix_new);
    component_register("dynamic-mix", dynamic_mix_new);
    component_register("balancer", balancer_new);
    component_register("op-amp", opamp_new);
    component_register("controller", controller_new);
    component_register("smooth", smooth_new);
    component_register("midi-note", midinote_new);
    component_register("echo", echo_new);
}
