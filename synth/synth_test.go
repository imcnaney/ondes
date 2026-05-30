package synth

import "testing"

// stubPatch builds a trivial voice: a configurable number of phase clocks
// plus one constant output wire into the voice mix. It satisfies the
// Patch interface (Apply(*Voice) error) directly, so these tests don't
// need the patch/component packages.
type stubPatch struct{ clocks int }

func (p stubPatch) Apply(v *Voice) error {
	for i := 0; i < p.clocks; i++ {
		v.AddPhaseClock()
	}
	w := v.NewWire(func() float64 { return 0.25 })
	v.AddVoiceMixInput(w)
	return nil
}

// TestVoiceKeyingByChannelAndNote checks that the same note on two
// channels yields two independent voices (the multi-timbral re-key).
func TestVoiceKeyingByChannelAndNote(t *testing.T) {
	s := New(44100, stubPatch{})
	s.NoteOn(0, 60, 100)
	s.NoteOn(1, 60, 100) // same note number, different channel
	if got := s.ActiveVoices(); got != 2 {
		t.Fatalf("same note on two channels: want 2 voices, got %d", got)
	}
	s.NoteOff(0, 60)
	if got := s.ActiveVoices(); got != 1 {
		t.Fatalf("after ch0 note-off: want 1 voice, got %d", got)
	}
	s.NoteOff(1, 60)
	if got := s.ActiveVoices(); got != 0 {
		t.Fatalf("after both note-offs: want 0 voices, got %d", got)
	}
}

// TestPhaseClocksReleasedWithVoice guards the live-session leak fix: a
// finished voice must unregister its phase clocks from the Instant, so
// the tick list doesn't grow unbounded across notes.
func TestPhaseClocksReleasedWithVoice(t *testing.T) {
	s := New(44100, stubPatch{clocks: 3})
	if got := s.Instant().ActiveClocks(); got != 0 {
		t.Fatalf("baseline: want 0 clocks, got %d", got)
	}
	s.NoteOn(0, 60, 100)
	s.NoteOn(0, 64, 100)
	if got := s.Instant().ActiveClocks(); got != 6 {
		t.Fatalf("two 3-clock voices: want 6 clocks, got %d", got)
	}
	s.NoteOff(0, 60)
	s.NoteOff(0, 64)
	if got := s.Instant().ActiveClocks(); got != 0 {
		t.Fatalf("clocks leaked: want 0 after both notes off, got %d", got)
	}
}

// TestChannelPatchOverride checks that SetChannelPatch routes notes on a
// channel through its own patch while other channels use the default.
func TestChannelPatchOverride(t *testing.T) {
	s := New(44100, stubPatch{clocks: 1})
	s.SetChannelPatch(2, stubPatch{clocks: 4})
	s.NoteOn(0, 60, 100) // default patch -> 1 clock
	s.NoteOn(2, 60, 100) // channel 2 patch -> 4 clocks
	if got := s.Instant().ActiveClocks(); got != 5 {
		t.Fatalf("want 5 clocks (1 default + 4 on ch2), got %d", got)
	}
}

// TestNilDefaultPatchDropsNote checks that a channel with no patch (and no
// default) silently drops notes instead of panicking.
func TestNilDefaultPatchDropsNote(t *testing.T) {
	s := New(44100, nil)
	s.NoteOn(0, 60, 100)
	if got := s.ActiveVoices(); got != 0 {
		t.Fatalf("nil patch should drop the note: want 0 voices, got %d", got)
	}
}
