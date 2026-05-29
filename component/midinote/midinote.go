// Package midinote implements the `midi-note` component: emits per-note
// frequency (linear-out) or note number (log-out) onto a named pin of
// another component. Mirrors ondes.synth.wire.MidiNoteNum.
//
// The "out:" target is given inside the nested linear-out / log-out
// blocks, not at the top level. Patch resolution looks for these blocks
// in a post-pass (see patch.go).
package midinote

import (
	"math"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("midi-note", func() component.Component { return &MidiNote{} })
}

// FreqTable bounds, matching Java FreqTable.getFreq(0)..getFreq(127).
// The Java table is generated from 440 * 2^((n-69)/12).
var (
	minFreq = 440 * math.Pow(2, (0-69)/12.0)
	maxFreq = 440 * math.Pow(2, (127-69)/12.0)
)

type MidiNote struct {
	voice *synth.Voice

	linearOut *synth.Wire
	logOut    *synth.Wire
	linearAmp float64
	logAmp    float64

	scaledLinear float64
	scaledLog    float64
}

func (m *MidiNote) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	m.voice = v
	if lin, ok := spec["linear-out"].(map[string]any); ok {
		if amp, ok := numeric(lin["amp"]); ok {
			m.linearAmp = amp
			m.linearOut = v.NewWire(m.computeLinear)
		}
	}
	if lg, ok := spec["log-out"].(map[string]any); ok {
		if amp, ok := numeric(lg["amp"]); ok {
			m.logAmp = amp
			m.logOut = v.NewWire(m.computeLog)
		}
	}
	return nil
}

// Output is unused: midi-note's wiring is via named outputs (LinearOut /
// LogOut), addressed during the patch's post-wire pass.
func (m *MidiNote) Output() *synth.Wire { return nil }

func (m *MidiNote) LinearOut() *synth.Wire { return m.linearOut }
func (m *MidiNote) LogOut() *synth.Wire    { return m.logOut }

func (m *MidiNote) computeLinear() float64 { return m.scaledLinear }
func (m *MidiNote) computeLog() float64    { return m.scaledLog }

func (m *MidiNote) OnMidi(msg synth.MidiMsg) {
	if !msg.IsNoteOn() {
		return
	}
	note := float64(msg.Data1)
	freq := 440 * math.Pow(2, (note-69)/12)
	if m.linearOut != nil {
		// Java: scaledLinear = freq / (maxFreq - minFreq) * linearOut, in int units.
		// Our float convention is 1.0 == 32767 Java units.
		m.scaledLinear = freq / (maxFreq - minFreq) * m.linearAmp / 32767.0
	}
	if m.logOut != nil {
		m.scaledLog = (note / 128) * m.logAmp / 32767.0
	}
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
