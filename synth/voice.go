package synth

import "math"

// MidiMsg is a parsed 3-byte channel message.
type MidiMsg struct {
	Status byte
	Data1  byte
	Data2  byte
}

func (m MidiMsg) IsNoteOn() bool {
	return m.Status&0xF0 == 0x90 && m.Data2 > 0
}

func (m MidiMsg) IsNoteOff() bool {
	return m.Status&0xF0 == 0x80 || (m.Status&0xF0 == 0x90 && m.Data2 == 0)
}

// MidiListener is implemented by components that subscribe to channel messages.
type MidiListener interface {
	OnMidi(m MidiMsg)
}

// Voice is one instantiation of a patch, currently playing one MIDI note.
type Voice struct {
	Note     uint8
	Chan     uint8
	Velocity uint8

	synth      *Synth
	components map[string]any
	wires      []*Wire
	voiceMix   *Junction
}

func newVoice(s *Synth, ch, note, vel uint8) *Voice {
	v := &Voice{
		Note:       note,
		Chan:       ch,
		Velocity:   vel,
		synth:      s,
		components: map[string]any{},
	}
	v.voiceMix = NewJunction(v)
	return v
}

func (v *Voice) Synth() *Synth { return v.synth }

// NoteFreq returns the equal-tempered frequency for this voice's MIDI note.
func (v *Voice) NoteFreq() float64 {
	return 440 * math.Pow(2, (float64(v.Note)-69)/12)
}

func (v *Voice) NewWire(compute func() float64) *Wire {
	w := NewWire(compute)
	v.wires = append(v.wires, w)
	return w
}

// AddVoiceMixInput plugs a wire into this voice's main summing junction;
// patches use this when a component declares `out: main`.
func (v *Voice) AddVoiceMixInput(w *Wire) {
	v.voiceMix.AddInput(w)
}

// MainOutput is the voice's contribution to the channel/main mix.
func (v *Voice) MainOutput() *Wire { return v.voiceMix.Output() }

func (v *Voice) ResetWires() {
	for _, w := range v.wires {
		w.Reset()
	}
}

func (v *Voice) AddComponent(name string, c any) {
	v.components[name] = c
}

func (v *Voice) Component(name string) any { return v.components[name] }

func (v *Voice) DispatchMidi(m MidiMsg) {
	for _, c := range v.components {
		if ml, ok := c.(MidiListener); ok {
			ml.OnMidi(m)
		}
	}
}
