package synth

import "math"

// silenceThreshold is the voice-output magnitude below which a sample
// rounds to int16 zero (1/32767). endingZeros is how many consecutive
// such samples mark a draining voice as truly finished - matching Java's
// WaveMonoMainMix, which stops the render after 100 zero output samples.
const (
	silenceThreshold = 1.0 / 32767.0
	endingZeros      = 100
)

// Patch is what a YAML file deserializes to: a set of named component
// specs that can be applied (instantiated) into a fresh Voice.
type Patch interface {
	Apply(v *Voice) error
}

// Synth is the engine: sample clock, active voices, main mix accumulator.
// It is intentionally minimal for now - one channel, no voice pool.
// Notes are created on NoteOn; they are removed on NoteOff, or - for
// exit-envelope voices - once their effect tail has rung out to silence.
type Synth struct {
	sr      int
	patch   Patch
	instant *Instant
	limiter *Limiter

	voices map[uint8]*Voice

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
		// Retrigger: dispatch but keep the voice. Cancel any drain so a
		// re-struck note that was tailing out plays in full again.
		existing.Velocity = vel
		existing.draining = false
		existing.zeroCount = 0
		existing.DispatchMidi(MidiMsg{Status: 0x90 | ch, Data1: note, Data2: vel})
		return
	}
	v := newVoice(s, ch, note, vel)
	if err := s.patch.Apply(v); err != nil {
		// Patch failed; drop this note. Real engine will log.
		return
	}
	s.voices[note] = v
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
// when a voice has finished sounding at its source. The voice is not
// removed immediately: it keeps rendering so any downstream effect tail
// (echo) rings out, and Step removes it once its output goes silent.
func (s *Synth) QueueNoteEnd(_, note uint8) {
	if v, ok := s.voices[note]; ok {
		v.StartDraining()
	}
}

func (s *Synth) removeVoice(_, note uint8) {
	delete(s.voices, note)
}

// Step advances one sample and returns the limited mix in [-1, +1].
func (s *Synth) Step() float64 {
	s.instant.Next()
	for _, v := range s.voices {
		v.ResetWires()
	}
	var sum float64
	for note, v := range s.voices {
		// The wire latches per sample, so reading the voice output here
		// returns the same value summed into the mix; no recomputation.
		out := v.MainOutput().Get()
		sum += out
		if !v.draining {
			continue
		}
		if math.Abs(out) < silenceThreshold {
			v.zeroCount++
		} else {
			v.zeroCount = 0
		}
		if v.zeroCount > endingZeros {
			s.pendingEnds = append(s.pendingEnds, pendingEnd{v.Chan, note})
		}
	}
	for _, pe := range s.pendingEnds {
		s.removeVoice(pe.ch, pe.note)
	}
	s.pendingEnds = s.pendingEnds[:0]
	return s.limiter.Apply(sum)
}

func (s *Synth) ActiveVoices() int { return len(s.voices) }
