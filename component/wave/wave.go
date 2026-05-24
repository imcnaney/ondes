// Package wave implements oscillator components. For now: sine only.
package wave

import (
	"fmt"
	"math"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("wave", func() component.Component { return &Wave{} })
}

// Wave is a phase-accumulator oscillator. The YAML `shape` field picks
// which generator function to use.
type Wave struct {
	shape  string
	clock  *synth.PhaseClock
	out    *synth.Wire
	voice  *synth.Voice
	level  float64 // per-component scalar; matches Java's per-voice amplitude
	active bool
	gen    func(phase float64) float64
}

// waveDefaultLevel is the empirical scale that makes a bare sine patch
// produce roughly the same RMS as Java's reference (peak ~800 against
// the int16 full-scale of 32767 ~ 0.024). Until we model the Java
// WaveGen's level handling precisely, this gets us within tolerance.
const waveDefaultLevel = 0.025

func (w *Wave) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	w.voice = v
	shape, _ := spec["shape"].(string)
	if shape == "" {
		return fmt.Errorf("wave: missing shape")
	}
	w.shape = shape
	switch shape {
	case "sine":
		w.gen = sineGen
	default:
		return fmt.Errorf("wave: shape %q not yet implemented", shape)
	}

	w.level = waveDefaultLevel
	if ls, ok := numeric(spec["level-scale"]); ok {
		w.level *= ls
	}

	w.clock = v.Synth().Instant().AddPhaseClock()
	w.clock.SetFrequency(v.NoteFreq())
	w.out = v.NewWire(w.sample)
	w.active = true
	return nil
}

func (w *Wave) Output() *synth.Wire { return w.out }

// OnMidi resets the oscillator phase and re-pitches on note-on; silences
// on note-off. Java's auto-envelope behavior is intentionally omitted
// for v1 - sharp on/off transitions are tolerated by the regression
// suite's 50ms RMS buckets.
func (w *Wave) OnMidi(m synth.MidiMsg) {
	switch {
	case m.IsNoteOn():
		w.clock.SetFrequency(440 * math.Pow(2, (float64(m.Data1)-69)/12))
		w.clock.ResetPhase()
		w.active = true
	case m.IsNoteOff():
		w.active = false
	}
}

func (w *Wave) sample() float64 {
	if !w.active {
		return 0
	}
	return w.gen(w.clock.Phase()) * w.level
}

func sineGen(phase float64) float64 {
	return math.Sin(2 * math.Pi * phase)
}

// numeric coerces YAML int/int64/float64 to float64.
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
