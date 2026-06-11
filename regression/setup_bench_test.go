package regression

import (
	"testing"

	"ondes/audio"
	"ondes/patch"
	"ondes/synth"
)

// BenchmarkNoteOnSetup measures the per-note voice-setup cost on the audio
// thread: newVoice + patch.Apply (Make + Configure + wire-resolve every
// component). This is exactly the work Java's ChannelVoicePool pre-pays at
// startup. A fresh Synth per iteration guarantees the cold-allocation path
// (no retrigger). ReportAllocs surfaces the allocation volume per note,
// which is the GC-pressure side of the same question.
//
// Run: go test ./regression -run=^$ -bench=NoteOnSetup -benchmem
func BenchmarkNoteOnSetup(b *testing.B) {
	if err := chdirModuleRoot(); err != nil {
		b.Fatalf("locating module root: %v", err)
	}

	// A light -> heavy spread. Heaviness comes from harmonic partial count
	// and filter-coefficient setup, not raw component count.
	patches := []string{"sine", "saw", "bell-organ", "bassoon", "brass", "ocean2"}

	for _, name := range patches {
		p, err := patch.Load(name)
		if err != nil {
			b.Fatalf("load %q: %v", name, err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s := synth.New(audio.SampleRate, p)
				s.NoteOn(0, 60, 100)
			}
		})
	}
}
