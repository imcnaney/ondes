// Standard MIDI File reader: parses a metric (ticks-per-quarter) SMF and
// returns its note-on/off and control-change messages stamped with the
// sample at which the renderer should dispatch them. SMPTE timing is
// rejected, matching the Go port.
#ifndef ONDES_SMF_H
#define ONDES_SMF_H

#include <stddef.h>
#include <stdint.h>

#include "ondes/midi.h"

typedef struct {
    int64_t sample;
    MidiMsg msg;
} MidiEvent;

// smf_read loads path and returns a malloc'd, time-sorted array of events
// (*n set to the count). Returns NULL on error with a message in err.
// Caller frees the returned array.
MidiEvent *smf_read(const char *path, int sample_rate, size_t *n, char *err,
                    size_t errlen);

#endif // ONDES_SMF_H
