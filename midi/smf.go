// Package midi reads SMF (Standard MIDI File) and converts events to
// sample-aligned messages the synth can consume.
package midi

import (
	"fmt"
	"os"
	"sort"

	"gitlab.com/gomidi/midi/v2/smf"

	"ondes/synth"
)

// Event is a single MIDI message stamped with the sample at which the
// renderer should dispatch it.
type Event struct {
	Sample int64
	Msg    synth.MidiMsg
}

// ReadFile loads an SMF file and returns its note-on/note-off events
// converted to sample positions at the given sample rate. Tempo meta
// events are honored; the file must use metric (ticks-per-quarter)
// timing - SMPTE-coded files are not yet supported.
func ReadFile(path string, sampleRate int) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s, err := smf.ReadFrom(f)
	if err != nil {
		return nil, fmt.Errorf("midi: %w", err)
	}

	ppq, ok := s.TimeFormat.(smf.MetricTicks)
	if !ok {
		return nil, fmt.Errorf("midi: SMPTE time format not supported")
	}

	type tickEvent struct {
		tick uint64
		msg  smf.Message
	}
	var all []tickEvent
	for _, track := range s.Tracks {
		var abs uint64
		for _, ev := range track {
			abs += uint64(ev.Delta)
			all = append(all, tickEvent{tick: abs, msg: ev.Message})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].tick < all[j].tick })

	const defaultBPM = 120.0
	bpm := defaultBPM
	var lastTick uint64
	var curSec float64
	tickToSec := func(delta uint64) float64 {
		return float64(delta) * 60.0 / (bpm * float64(ppq))
	}

	var out []Event
	for _, te := range all {
		curSec += tickToSec(te.tick - lastTick)
		lastTick = te.tick

		var newBPM float64
		if te.msg.GetMetaTempo(&newBPM) {
			bpm = newBPM
			continue
		}

		var ch, key, vel, cc, ccVal uint8
		switch {
		case te.msg.GetNoteOn(&ch, &key, &vel):
			out = append(out, Event{
				Sample: int64(curSec*float64(sampleRate) + 0.5),
				Msg:    synth.MidiMsg{Status: 0x90 | ch, Data1: key, Data2: vel},
			})
		case te.msg.GetNoteOff(&ch, &key, &vel):
			out = append(out, Event{
				Sample: int64(curSec*float64(sampleRate) + 0.5),
				Msg:    synth.MidiMsg{Status: 0x80 | ch, Data1: key, Data2: 0},
			})
		case te.msg.GetControlChange(&ch, &cc, &ccVal):
			out = append(out, Event{
				Sample: int64(curSec*float64(sampleRate) + 0.5),
				Msg:    synth.MidiMsg{Status: 0xB0 | ch, Data1: cc, Data2: ccVal},
			})
		}
	}
	return out, nil
}
