// Component is anything that participates in a Voice's audio graph. The
// vtable mirrors the Go interfaces: every component has configure+output;
// add_input (Inputter), on_midi (MidiListener) and named_output
// (LinearOut/LogOut/LevelOut) are optional - a NULL slot means the
// component does not support that role, exactly like a failed Go type
// assertion.
//
// Concrete components embed `Component base;` as their first member and
// cast Component* <-> their own struct.
#ifndef ONDES_COMPONENT_H
#define ONDES_COMPONENT_H

#include "ondes/arena.h"
#include "ondes/midi.h"
#include "ondes/spec.h"
#include "ondes/voice.h"
#include "ondes/wire.h"

typedef struct Component Component;

typedef struct ComponentVTable {
    int (*configure)(Component *self, const Spec *spec, Voice *v,
                     const char *name);
    Wire *(*output)(Component *self);
    void (*add_input)(Component *self, const char *sel, Wire *src); // optional
    void (*on_midi)(Component *self, MidiMsg m);                    // optional
    Wire *(*named_output)(Component *self, const char *key);        // optional
} ComponentVTable;

struct Component {
    const ComponentVTable *vt;
};

// --- dispatch helpers ---
static inline int component_configure(Component *c, const Spec *spec, Voice *v,
                                      const char *name) {
    return c->vt->configure(c, spec, v, name);
}
static inline Wire *component_output(Component *c) { return c->vt->output(c); }
static inline bool component_add_input(Component *c, const char *sel,
                                       Wire *src) {
    if (c->vt->add_input) {
        c->vt->add_input(c, sel, src);
        return true;
    }
    return false;
}
static inline void component_on_midi(Component *c, MidiMsg m) {
    if (c->vt->on_midi) c->vt->on_midi(c, m);
}
static inline Wire *component_named_output(Component *c, const char *key) {
    return c->vt->named_output ? c->vt->named_output(c, key) : NULL;
}

// --- registry ---
typedef Component *(*ComponentFactory)(Arena *a);

// component_register associates a YAML `type` value with a constructor.
// Components register from component_register_all (called at startup).
void component_register(const char *type, ComponentFactory f);

// component_make returns a fresh component for the type (allocated from a),
// or NULL if the type is unknown.
Component *component_make(Arena *a, const char *type);

// component_register_all registers every built-in component type. Call
// once before loading patches (the C analogue of Go's blank imports).
void component_register_all(void);

#endif // ONDES_COMPONENT_H
