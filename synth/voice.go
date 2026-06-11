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
	clocks     []*PhaseClock // phase clocks this voice owns, released on teardown
	voiceMix   *Junction
	waitForEnv bool // if true, note-off does not immediately remove the voice

	// draining is set once an exit-envelope has reached zero: the voice
	// is finished sounding directly, but downstream effects (echo) may
	// still be ringing. While draining, the voice stays in the mix until
	// its output has been silent for endingZeros consecutive samples.
	draining  bool
	zeroCount int
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

// AddPhaseClock creates a phase clock owned by this voice. Components must
// allocate their oscillator clocks through here (rather than reaching for
// the Instant directly) so that ReleaseClocks can unregister them when the
// voice is torn down, keeping the Instant's tick list bounded to live
// voices.
func (v *Voice) AddPhaseClock() *PhaseClock {
	pc := v.synth.instant.AddPhaseClock()
	v.clocks = append(v.clocks, pc)
	return pc
}

// ReleaseClocks unregisters this voice's phase clocks from the Instant.
// Called by the synth when the voice is removed.
func (v *Voice) ReleaseClocks() {
	for _, pc := range v.clocks {
		v.synth.instant.RemoveClock(pc)
	}
	v.clocks = nil
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

// SetWaitForEnv lets a component (typically env with exit: true) ask
// the synth to keep the voice alive past note-off until the envelope
// reports completion via Synth.QueueNoteEnd.
func (v *Voice) SetWaitForEnv(b bool) { v.waitForEnv = b }

func (v *Voice) WaitForEnv() bool { return v.waitForEnv }

// StartDraining marks the voice as finished at the source; it stays in
// the mix until its tail (echo, reverb) decays to silence.
func (v *Voice) StartDraining() { v.draining = true }
