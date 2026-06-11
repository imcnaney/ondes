package synth

import (
	"log"
	"math"
)

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
// It is multi-timbral: each MIDI channel plays its own patch (a default
// patch covers channels with no explicit assignment), and voices are
// keyed by (channel, note) so the same note on two channels is two
// distinct voices. Notes are created on NoteOn; they are removed on
// NoteOff, or - for exit-envelope voices - once their effect tail has
// rung out to silence.
type Synth struct {
	sr      int
	instant *Instant
	limiter *Limiter

	// defaultPatch covers any channel without an explicit patch;
	// patches overrides it per channel for multi-timbral playback.
	defaultPatch Patch
	patches      map[uint8]Patch

	// voices is keyed by voiceKey(channel, note).
	voices map[uint16]*Voice

	// pendingEnds is drained at the top of each Step. Components (env
	// with exit: true) queue voice removals here rather than mutating
	// the voice map mid-iteration.
	pendingEnds []pendingEnd

	// applyErrLogged suppresses repeat logging when a patch fails to
	// apply: the same patch fails identically on every note, so we log
	// the first occurrence and stay quiet thereafter.
	applyErrLogged bool

	// ccState holds the most recent control-change value per (channel,
	// controller). Java controllers are channel-context and persist
	// across notes; our voices are per-note, so when a voice is created
	// we replay the channel's current CC state into it. Keyed by
	// uint16(channel)<<8 | controller.
	ccState map[uint16]uint8
}

type pendingEnd struct{ ch, note uint8 }

// voiceKey packs a (channel, note) pair into the voices-map key.
func voiceKey(ch, note uint8) uint16 { return uint16(ch)<<8 | uint16(note) }

func New(sr int, patch Patch) *Synth {
	return &Synth{
		sr:           sr,
		instant:      NewInstant(sr),
		limiter:      NewLimiter(sr),
		defaultPatch: patch,
		patches:      map[uint8]Patch{},
		voices:       map[uint16]*Voice{},
		ccState:      map[uint16]uint8{},
	}
}

func (s *Synth) SampleRate() int   { return s.sr }
func (s *Synth) Instant() *Instant { return s.instant }

// SetChannelPatch assigns a patch to a single MIDI channel, overriding
// the default patch for notes that arrive on that channel. This is how a
// multi-timbral setup (different instrument per channel) is configured.
func (s *Synth) SetChannelPatch(ch uint8, p Patch) { s.patches[ch] = p }

// patchFor returns the patch that should voice notes on the given channel.
func (s *Synth) patchFor(ch uint8) Patch {
	if p, ok := s.patches[ch]; ok {
		return p
	}
	return s.defaultPatch
}

func (s *Synth) NoteOn(ch, note, vel uint8) {
	k := voiceKey(ch, note)
	if existing, ok := s.voices[k]; ok {
		// Retrigger: dispatch but keep the voice. Cancel any drain so a
		// re-struck note that was tailing out plays in full again.
		existing.Velocity = vel
		existing.draining = false
		existing.zeroCount = 0
		existing.DispatchMidi(MidiMsg{Status: 0x90 | ch, Data1: note, Data2: vel})
		return
	}
	p := s.patchFor(ch)
	if p == nil {
		return // no patch assigned to this channel
	}
	v := newVoice(s, ch, note, vel)
	if err := p.Apply(v); err != nil {
		// Patch failed (e.g. an unknown component type); drop this note.
		// Log once - the same patch fails the same way on every note.
		if !s.applyErrLogged {
			log.Printf("synth: dropping notes, patch failed to apply: %v", err)
			s.applyErrLogged = true
		}
		return
	}
	s.voices[k] = v
	v.DispatchMidi(MidiMsg{Status: 0x90 | ch, Data1: note, Data2: vel})
	// Replay the channel's current controller state so a voice created
	// mid-sweep starts at the live CC value rather than zero.
	for ccKey, val := range s.ccState {
		if uint8(ccKey>>8) != ch {
			continue
		}
		v.DispatchMidi(MidiMsg{Status: 0xB0 | ch, Data1: uint8(ccKey), Data2: val})
	}
}

// ControlChange records a control-change value for the channel and
// dispatches it to that channel's active voices, so controller
// components update. Voices on other channels are unaffected.
func (s *Synth) ControlChange(ch, cc, val uint8) {
	s.ccState[uint16(ch)<<8|uint16(cc)] = val
	msg := MidiMsg{Status: 0xB0 | ch, Data1: cc, Data2: val}
	for _, v := range s.voices {
		if v.Chan == ch {
			v.DispatchMidi(msg)
		}
	}
}

func (s *Synth) NoteOff(ch, note uint8) {
	v, ok := s.voices[voiceKey(ch, note)]
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
func (s *Synth) QueueNoteEnd(ch, note uint8) {
	if v, ok := s.voices[voiceKey(ch, note)]; ok {
		v.StartDraining()
	}
}

func (s *Synth) removeVoice(ch, note uint8) {
	k := voiceKey(ch, note)
	if v, ok := s.voices[k]; ok {
		v.ReleaseClocks() // stop the Instant from ticking a dead voice's clocks
		delete(s.voices, k)
	}
}

// Step advances one sample and returns the limited mix in [-1, +1].
func (s *Synth) Step() float64 {
	s.instant.Next()
	for _, v := range s.voices {
		v.ResetWires()
	}
	var sum float64
	for _, v := range s.voices {
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
			s.pendingEnds = append(s.pendingEnds, pendingEnd{v.Chan, v.Note})
		}
	}
	for _, pe := range s.pendingEnds {
		s.removeVoice(pe.ch, pe.note)
	}
	s.pendingEnds = s.pendingEnds[:0]
	return s.limiter.Apply(sum)
}

func (s *Synth) ActiveVoices() int { return len(s.voices) }
