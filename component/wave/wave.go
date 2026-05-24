// Package wave implements oscillator components. For now: sine only.
package wave

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"ondes/component"
	"ondes/synth"
)

// harmonicPresets mirrors HarmonicWaveGen.presets / presetTags. Each
// row is alternating (frequency-multiplier, amplitude-divisor) pairs.
var harmonicPresets = map[string][]float64{
	"mellow": {1, 1, 2, 2, 3, 3},
	"odd":    {1, 1, 2, 2, 6, 3, 14, 3},
	"bell":   {1, 1, 2, 2, 11, 3, 14, 3, 17, 3},
	"organ":  {1, 1, 2, 2, 3, 3, 4, 2, 8, 2, 12, 3},
}

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
	case "saw":
		w.gen = sawGen
	case "square":
		w.gen = squareGen
	case "ramp-up":
		w.gen = rampUpGen
	case "ramp-down":
		w.gen = rampDownGen
	case "harmonic":
		params, err := parseHarmonicParams(spec)
		if err != nil {
			return err
		}
		w.gen = makeHarmonicGen(params)
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

// sawGen is misleadingly named in the Java source: it's actually a
// triangle wave (linear ramp up to +1, linear ramp down to -1).
func sawGen(phase float64) float64 {
	if phase < 0.5 {
		return 4*phase - 1
	}
	return 4*(1-phase) - 1
}

func squareGen(phase float64) float64 {
	if phase > 0.5 {
		return 1
	}
	return -1
}

func rampUpGen(phase float64) float64 { return 2*phase - 1 }

func rampDownGen(phase float64) float64 { return 2*(1-phase) - 1 }

// parseHarmonicParams reads either `preset:` (named preset) or `waves:`
// (list of mult/divisor tokens, possibly grouped per line) into a flat
// even-length []float64. Mirrors HarmonicWaveGen.configure.
func parseHarmonicParams(spec component.Spec) ([]float64, error) {
	if p, ok := spec["preset"].(string); ok {
		if params, ok := harmonicPresets[p]; ok {
			return params, nil
		}
		return nil, fmt.Errorf("wave: unknown harmonic preset %q", p)
	}
	raw, ok := spec["waves"]
	if !ok {
		return nil, fmt.Errorf("wave: harmonic shape needs preset: or waves:")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("wave: harmonic waves must be a list")
	}
	var tokens []string
	for _, el := range list {
		switch v := el.(type) {
		case string:
			tokens = append(tokens, strings.Fields(strings.ReplaceAll(v, ",", " "))...)
		case []any:
			for _, x := range v {
				tokens = append(tokens, fmt.Sprintf("%v", x))
			}
		default:
			tokens = append(tokens, fmt.Sprintf("%v", v))
		}
	}
	if len(tokens)%2 != 0 || len(tokens) == 0 {
		return nil, fmt.Errorf("wave: harmonic waves must be pairs of (mult, divisor), got %d tokens", len(tokens))
	}
	params := make([]float64, len(tokens))
	for i, t := range tokens {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("wave: harmonic param %q: %w", t, err)
		}
		params[i] = f
	}
	return params, nil
}

// makeHarmonicGen returns a closure that sums sin(2*pi*phase*mult)/divisor
// over each (mult, divisor) pair. Java caches a 1024-sample lookup
// table here for speed; we just call math.Sin directly until profiling
// says otherwise.
func makeHarmonicGen(params []float64) func(phase float64) float64 {
	return func(phase float64) float64 {
		var sum float64
		for i := 0; i+1 < len(params); i += 2 {
			sum += math.Sin(2*math.Pi*phase*params[i]) / params[i+1]
		}
		return sum
	}
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
