// CoreMIDI input for the live synth. Lists sources, connects to one by
// substring, and parses the raw MIDI byte stream (with running status)
// from each incoming packet list into channel messages.
//
// MIDIInputPortCreate / MIDIReadProc are deprecated in favour of the
// protocol-aware variants on recent macOS but remain fully functional and
// are far simpler (raw MIDIPacketList bytes); the deprecation warning is
// suppressed below.
#include "ondes/mididev.h"

#include <stdio.h>
#include <stdlib.h>
#include <strings.h>

#include <CoreMIDI/CoreMIDI.h>

#pragma clang diagnostic ignored "-Wdeprecated-declarations"

struct MidiIn {
    MIDIClientRef client;
    MIDIPortRef port;
    MidiMsgFn cb;
    void *user;

    // running parser state (a message may, in theory, span packets)
    uint8_t status;
    uint8_t data[2];
    int data_count;
    int need;
    int in_sysex;
};

// source_name copies a MIDI endpoint's display name into buf.
static void source_name(MIDIEndpointRef ep, char *buf, size_t n) {
    CFStringRef s = NULL;
    if (MIDIObjectGetStringProperty(ep, kMIDIPropertyDisplayName, &s) ==
            noErr &&
        s) {
        if (!CFStringGetCString(s, buf, (CFIndex)n, kCFStringEncodingUTF8))
            snprintf(buf, n, "(unnamed)");
        CFRelease(s);
    } else {
        snprintf(buf, n, "(unnamed)");
    }
}

int midi_list_inputs(void) {
    ItemCount count = MIDIGetNumberOfSources();
    printf("MIDI input ports:\n");
    if (count == 0) {
        printf("  (none)\n");
        return 0;
    }
    for (ItemCount i = 0; i < count; i++) {
        char name[256];
        source_name(MIDIGetSource(i), name, sizeof(name));
        printf("  - %s\n", name);
    }
    return 0;
}

static void emit(MidiIn *m, uint8_t status, uint8_t d1, uint8_t d2) {
    m->cb(status, d1, d2, m->user);
}

// parse_bytes feeds raw MIDI bytes through the running-status state machine.
static void parse_bytes(MidiIn *m, const uint8_t *b, int len) {
    for (int i = 0; i < len; i++) {
        uint8_t v = b[i];
        if (v >= 0xF8) continue; // real-time, ignore (doesn't affect status)
        if (v >= 0x80) {
            if (v >= 0xF0) {
                // system common / sysex
                if (v == 0xF0) m->in_sysex = 1;
                if (v == 0xF7) m->in_sysex = 0;
                m->status = 0;
                m->data_count = 0;
                continue;
            }
            m->status = v;
            uint8_t hi = v & 0xF0;
            m->need = (hi == 0xC0 || hi == 0xD0) ? 1 : 2;
            m->data_count = 0;
            continue;
        }
        if (m->in_sysex || m->status == 0) continue;
        m->data[m->data_count++] = v;
        if (m->data_count >= m->need) {
            emit(m, m->status, m->data[0], m->need == 2 ? m->data[1] : 0);
            m->data_count = 0; // running status: keep m->status
        }
    }
}

static void read_proc(const MIDIPacketList *pktlist, void *refcon,
                      void *src_refcon) {
    (void)src_refcon;
    MidiIn *m = refcon;
    const MIDIPacket *p = &pktlist->packet[0];
    for (UInt32 i = 0; i < pktlist->numPackets; i++) {
        parse_bytes(m, p->data, p->length);
        p = MIDIPacketNext(p);
    }
}

MidiIn *midi_open(const char *substr, MidiMsgFn cb, void *user, char *label,
                  size_t labellen, char *err, size_t errlen) {
    ItemCount count = MIDIGetNumberOfSources();
    MIDIEndpointRef src = 0;
    int found = 0;
    for (ItemCount i = 0; i < count; i++) {
        MIDIEndpointRef ep = MIDIGetSource(i);
        char name[256];
        source_name(ep, name, sizeof(name));
        if (!substr || !*substr || strcasestr(name, substr)) {
            src = ep;
            snprintf(label, labellen, "%s", name);
            found = 1;
            break;
        }
    }
    if (!found) {
        snprintf(err, errlen, "no MIDI input matched \"%s\"",
                 substr ? substr : "");
        return NULL;
    }

    MidiIn *m = calloc(1, sizeof(*m));
    m->cb = cb;
    m->user = user;

    CFStringRef cname = CFStringCreateWithCString(NULL, "ondes", kCFStringEncodingUTF8);
    if (MIDIClientCreate(cname, NULL, NULL, &m->client) != noErr) {
        CFRelease(cname);
        snprintf(err, errlen, "MIDIClientCreate failed");
        free(m);
        return NULL;
    }
    CFStringRef pname = CFStringCreateWithCString(NULL, "ondes-in", kCFStringEncodingUTF8);
    if (MIDIInputPortCreate(m->client, pname, read_proc, m, &m->port) != noErr) {
        CFRelease(cname);
        CFRelease(pname);
        MIDIClientDispose(m->client);
        snprintf(err, errlen, "MIDIInputPortCreate failed");
        free(m);
        return NULL;
    }
    if (MIDIPortConnectSource(m->port, src, NULL) != noErr) {
        CFRelease(cname);
        CFRelease(pname);
        MIDIPortDispose(m->port);
        MIDIClientDispose(m->client);
        snprintf(err, errlen, "MIDIPortConnectSource failed");
        free(m);
        return NULL;
    }
    CFRelease(cname);
    CFRelease(pname);
    return m;
}

void midi_close(MidiIn *m) {
    if (!m) return;
    MIDIPortDispose(m->port);
    MIDIClientDispose(m->client);
    free(m);
}
