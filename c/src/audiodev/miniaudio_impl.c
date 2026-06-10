// Single translation unit that compiles the vendored miniaudio library.
// Only the live tools link this; the offline renderer stays dependency-free.
#define MA_NO_DECODING
#define MA_NO_ENCODING
#define MA_NO_GENERATION
#define MA_NO_RESOURCE_MANAGER
#define MINIAUDIO_IMPLEMENTATION
#include "miniaudio.h"
