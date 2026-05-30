package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"ondes/audio"
	"ondes/midi"
	"ondes/patch"
	"ondes/synth"
)

// These are fast smoke tests over the real render path (patch.Load ->
// synth -> CC dispatch) used by cmd/p. They guard against the two failure
// modes the port has hit before: an unregistered component renders pure
// silence, and a botched int/float scale conversion renders a near-silent
// signal because the CC modulation never reaches it. Full sample-accurate
// parity against the Java reference summaries lives in the
// `go test ./regression` suite (regression/regression_test.go); these
// just catch gross breakage quickly without shelling out to python.

func TestMain(m *testing.M) {
	// patch.Load resolves ./program relative to CWD; tests run from the
	// package dir, so hop up to the module root first.
	if err := chdirModuleRoot(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func chdirModuleRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return os.ErrNotExist
		}
		dir = parent
	}
}

// render plays a MIDI fixture through a patch and returns the absolute
// peak of the output in [0, 1] plus the fraction of non-zero samples.
func render(t *testing.T, patchName, midiName string) (peak, nonZeroFrac float64) {
	t.Helper()
	p, err := patch.Load(patchName)
	if err != nil {
		t.Fatalf("load %s: %v", patchName, err)
	}
	events, err := midi.ReadFile(filepath.Join("regression", "fixtures", "mid", midiName), audio.SampleRate)
	if err != nil {
		t.Fatalf("midi %s: %v", midiName, err)
	}
	if len(events) == 0 {
		t.Fatalf("%s: no events", midiName)
	}

	s := synth.New(audio.SampleRate, p)
	last := events[len(events)-1].Sample
	end := last + int64(0.25*float64(audio.SampleRate))

	var nonZero, total int64
	ei := 0
	for i := int64(0); i <= end || s.ActiveVoices() > 0; i++ {
		for ei < len(events) && events[ei].Sample <= i {
			m := events[ei].Msg
			ch := m.Status & 0x0F
			switch {
			case m.IsNoteOn():
				s.NoteOn(ch, m.Data1, m.Data2)
			case m.IsNoteOff():
				s.NoteOff(ch, m.Data1)
			case m.Status&0xF0 == 0xB0:
				s.ControlChange(ch, m.Data1, m.Data2)
			}
			ei++
		}
		out := s.Step()
		if a := math.Abs(out); a > peak {
			peak = a
		}
		if out != 0 {
			nonZero++
		}
		total++
		if i > last+int64(30*audio.SampleRate) { // safety cap
			break
		}
	}
	return peak, float64(nonZero) / float64(total)
}

func TestSmoothRenders(t *testing.T) {
	peak, nz := render(t, "smooth", "sustain.mid")
	// The CC-12 sweep drives compScale ~90x; without CC dispatch the
	// signal peaks around 0.03. A peak well above that proves the
	// controller modulation reaches the smoother.
	if peak < 0.1 {
		t.Errorf("smooth peak %.4f too low - CC modulation likely not reaching the smoother", peak)
	}
	if peak > 1.0 {
		t.Errorf("smooth peak %.4f exceeds full scale", peak)
	}
	if nz < 0.5 {
		t.Errorf("smooth non-zero fraction %.2f - output is mostly silent", nz)
	}
}

func TestBalancerRenders(t *testing.T) {
	peak, nz := render(t, "balancer", "sustain.mid")
	if peak <= 0 {
		t.Errorf("balancer is silent (peak %.4f) - component likely unregistered", peak)
	}
	if peak > 1.0 {
		t.Errorf("balancer peak %.4f exceeds full scale", peak)
	}
	if nz < 0.5 {
		t.Errorf("balancer non-zero fraction %.2f - output is mostly silent", nz)
	}
}

// ocean2 exercises the sinc filter (default-shape, note-tuned running
// average) and the pink-noise shape together; both render silence if
// either component is missing.
func TestOcean2Renders(t *testing.T) {
	peak, nz := render(t, "ocean2", "sustain.mid")
	if peak <= 0 {
		t.Errorf("ocean2 is silent (peak %.4f) - sinc filter or pink shape likely missing", peak)
	}
	if peak > 1.0 {
		t.Errorf("ocean2 peak %.4f exceeds full scale", peak)
	}
	if nz < 0.5 {
		t.Errorf("ocean2 non-zero fraction %.2f - output is mostly silent", nz)
	}
}
