// mididev is the platform-agnostic MIDI-input interface for the live synth.
// Backends implement it per OS with no third-party library: CoreMIDI on
// macOS (coremidi.c), winmm on Windows (winmm.c). Linux would add an ALSA
// backend behind this same interface. The build selects one per platform.
#ifndef ONDES_MIDIDEV_H
#define ONDES_MIDIDEV_H

#include <stddef.h>
#include <stdint.h>

// MidiMsgFn is called (on a CoreMIDI thread) for each parsed channel
// message. Only note-on/off and control-change are forwarded.
typedef void (*MidiMsgFn)(uint8_t status, uint8_t d1, uint8_t d2, void *user);

// midi_list_inputs prints the available MIDI input ports. Returns 0 on ok.
int midi_list_inputs(void);

typedef struct MidiIn MidiIn;

// midi_open connects to the first MIDI source whose name contains substr
// (case-insensitive) and delivers parsed messages to cb. The source name
// is written to label. Returns NULL on failure with a message in err.
MidiIn *midi_open(const char *substr, MidiMsgFn cb, void *user, char *label,
                  size_t labellen, char *err, size_t errlen);

void midi_close(MidiIn *m);

#endif // ONDES_MIDIDEV_H
