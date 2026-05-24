package synth

// Patch is what a YAML file deserializes to: a set of named component
// specs that can be applied (instantiated) into a fresh Voice.
type Patch interface {
	Apply(v *Voice) error
}

// Synth is the engine: sample clock, active voices, main mix accumulator.
// It is intentionally minimal for now - one channel, no voice pool,
// no envelope-driven note-end queue. Notes are created on NoteOn and
// destroyed on NoteOff.
type Synth struct {
	sr      int
	patch   Patch
	instant *Instant
	limiter *Limiter

	voices     map[uint8]*Voice
	mainInputs []*Wire

	// pendingEnds is drained at the top of each Step. Components (env
	// with exit: true) queue voice removals here rather than mutating
	// the voice map mid-iteration.
	pendingEnds []pendingEnd
}

type pendingEnd struct{ ch, note uint8 }

func New(sr int, patch Patch) *Synth {
	return &Synth{
		sr:      sr,
		patch:   patch,
		instant: NewInstant(sr),
		limiter: NewLimiter(sr),
		voices:  map[uint8]*Voice{},
	}
}

func (s *Synth) SampleRate() int   { return s.sr }
func (s *Synth) Instant() *Instant { return s.instant }

func (s *Synth) NoteOn(ch, note, vel uint8) {
	if existing, ok := s.voices[note]; ok {
		// Retrigger: dispatch but keep the voice.
		existing.Velocity = vel
		existing.DispatchMidi(MidiMsg{Status: 0x90 | ch, Data1: note, Data2: vel})
		return
	}
	v := newVoice(s, ch, note, vel)
	if err := s.patch.Apply(v); err != nil {
		// Patch failed; drop this note. Real engine will log.
		return
	}
	s.voices[note] = v
	s.mainInputs = append(s.mainInputs, v.MainOutput())
	v.DispatchMidi(MidiMsg{Status: 0x90 | ch, Data1: note, Data2: vel})
}

func (s *Synth) NoteOff(ch, note uint8) {
	v, ok := s.voices[note]
	if !ok {
		return
	}
	v.DispatchMidi(MidiMsg{Status: 0x80 | ch, Data1: note, Data2: 0})
	if v.WaitForEnv() {
		// Leave the voice connected; the envelope will queue removal
		// when it finishes its release phase.
		return
	}
	s.removeVoice(ch, note)
}

// QueueNoteEnd is called by envelopes (or other terminal components)
// when a voice has finished sounding. The removal happens at the top of
// the next Step.
func (s *Synth) QueueNoteEnd(ch, note uint8) {
	s.pendingEnds = append(s.pendingEnds, pendingEnd{ch, note})
}

func (s *Synth) removeVoice(_, note uint8) {
	v, ok := s.voices[note]
	if !ok {
		return
	}
	out := v.MainOutput()
	for i, w := range s.mainInputs {
		if w == out {
			s.mainInputs = append(s.mainInputs[:i], s.mainInputs[i+1:]...)
			break
		}
	}
	delete(s.voices, note)
}

// Step advances one sample and returns the limited mix in [-1, +1].
func (s *Synth) Step() float64 {
	for _, pe := range s.pendingEnds {
		s.removeVoice(pe.ch, pe.note)
	}
	s.pendingEnds = s.pendingEnds[:0]

	s.instant.Next()
	for _, v := range s.voices {
		v.ResetWires()
	}
	var sum float64
	for _, w := range s.mainInputs {
		sum += w.Get()
	}
	return s.limiter.Apply(sum)
}

func (s *Synth) ActiveVoices() int { return len(s.voices) }
