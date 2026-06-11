// MidiMsg is a parsed 3-byte channel message and the listener hook
// components implement to react to it.
#ifndef ONDES_MIDI_H
#define ONDES_MIDI_H

#include <stdbool.h>
#include <stdint.h>

typedef struct MidiMsg {
    uint8_t status;
    uint8_t data1;
    uint8_t data2;
} MidiMsg;

static inline bool midi_is_note_on(MidiMsg m) {
    return (m.status & 0xF0) == 0x90 && m.data2 > 0;
}

static inline bool midi_is_note_off(MidiMsg m) {
    return (m.status & 0xF0) == 0x80 ||
           ((m.status & 0xF0) == 0x90 && m.data2 == 0);
}

#endif // ONDES_MIDI_H
