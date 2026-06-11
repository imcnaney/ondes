// Windows MIDI input via the Win32 winmm API (midiInOpen + a callback),
// the counterpart of coremidi.c. Implements the same mididev.h interface,
// so cmd/o and the engine are unchanged. winmm delivers complete short
// messages (it expands running status itself), so the status/data bytes are
// just unpacked from the callback's dwParam1 - no stream parser needed.
//
// Untested: built and reasoned against the Win32 docs, but not run on a
// Windows machine. Target toolchain is MinGW-w64 (the engine uses C11
// stdatomic and clock_gettime/nanosleep, which MinGW-w64 provides and MSVC
// historically does not). Link with -lwinmm.
#include "ondes/mididev.h"

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <windows.h>
#include <mmsystem.h>

struct MidiIn {
    HMIDIIN h;
    MidiMsgFn cb;
    void *user;
};

// ci_contains: case-insensitive substring test (strcasestr isn't portable).
static int ci_contains(const char *hay, const char *needle) {
    if (!needle || !*needle) return 1;
    size_t nl = strlen(needle);
    for (; *hay; hay++) {
        size_t i = 0;
        while (i < nl && hay[i] &&
               tolower((unsigned char)hay[i]) == tolower((unsigned char)needle[i]))
            i++;
        if (i == nl) return 1;
    }
    return 0;
}

int midi_list_inputs(void) {
    UINT n = midiInGetNumDevs();
    printf("MIDI input ports:\n");
    if (n == 0) {
        printf("  (none)\n");
        return 0;
    }
    for (UINT i = 0; i < n; i++) {
        MIDIINCAPSA caps;
        if (midiInGetDevCapsA(i, &caps, sizeof(caps)) == MMSYSERR_NOERROR)
            printf("  - %s\n", caps.szPname);
    }
    return 0;
}

// midi_proc runs on a winmm callback thread. For a short channel message
// (MIM_DATA), dwParam1 packs status/data1/data2 in its low three bytes.
static void CALLBACK midi_proc(HMIDIIN h, UINT msg, DWORD_PTR inst,
                              DWORD_PTR p1, DWORD_PTR p2) {
    (void)h;
    (void)p2;
    if (msg != MIM_DATA) return; // ignore open/close/longdata/error
    MidiIn *m = (MidiIn *)inst;
    uint8_t status = (uint8_t)(p1 & 0xFF);
    uint8_t d1 = (uint8_t)((p1 >> 8) & 0xFF);
    uint8_t d2 = (uint8_t)((p1 >> 16) & 0xFF);
    m->cb(status, d1, d2, m->user);
}

MidiIn *midi_open(const char *substr, MidiMsgFn cb, void *user, char *label,
                  size_t labellen, char *err, size_t errlen) {
    UINT n = midiInGetNumDevs();
    int found = -1;
    for (UINT i = 0; i < n; i++) {
        MIDIINCAPSA caps;
        if (midiInGetDevCapsA(i, &caps, sizeof(caps)) != MMSYSERR_NOERROR)
            continue;
        if (!substr || !*substr || ci_contains(caps.szPname, substr)) {
            found = (int)i;
            snprintf(label, labellen, "%s", caps.szPname);
            break;
        }
    }
    if (found < 0) {
        snprintf(err, errlen, "no MIDI input matched \"%s\"",
                 substr ? substr : "");
        return NULL;
    }

    MidiIn *m = calloc(1, sizeof(*m));
    m->cb = cb;
    m->user = user;
    MMRESULT r = midiInOpen(&m->h, (UINT)found, (DWORD_PTR)midi_proc,
                            (DWORD_PTR)m, CALLBACK_FUNCTION);
    if (r != MMSYSERR_NOERROR) {
        snprintf(err, errlen, "midiInOpen failed (code %u)", r);
        free(m);
        return NULL;
    }
    midiInStart(m->h);
    return m;
}

void midi_close(MidiIn *m) {
    if (!m) return;
    midiInStop(m->h);
    midiInClose(m->h);
    free(m);
}
