// Patch loads a YAML synth program and applies it to fresh voices. The
// on-disk format mirrors the Java/Go synth: a top-level map of component
// name -> spec, where each spec has a `type` and an optional `out`
// directive. A loaded Patch is immutable and can voice many notes.
#ifndef ONDES_PATCH_H
#define ONDES_PATCH_H

#include <stddef.h>

#include "ondes/synth.h"

typedef struct Patch Patch;

// patch_load resolves a patch by name (searching ./program first, then
// the bundled src/main/resources/program), parses it, and returns it. On
// failure it returns NULL and writes a message to err (if non-NULL).
Patch *patch_load(const char *name, char *err, size_t errlen);

void patch_free(Patch *p);
const char *patch_name(const Patch *p);

// patch_as_synth_patch wraps the patch for synth_new / synth_set_channel_patch.
SynthPatch patch_as_synth_patch(Patch *p);

#endif // ONDES_PATCH_H
