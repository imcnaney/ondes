// Package wave implements oscillator components: simple periodic shapes
// (sine/saw/square/ramp), additive harmonic stacks, anharmonic stacks
// (one phase clock per partial), and noise.
package wave

import (
	"fmt"
	"math"
	"math/rand"
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
// which generator to use; some shapes (anharmonic) own additional phase
// clocks beyond the fundamental.
//
// The wave doesn't silence itself on note-off - the synth either drops
// the whole voice (bare wave patch) or an env in the same voice keeps
// it alive and attenuates the release tail.
type Wave struct {
	shape string
	clock *synth.PhaseClock // fundamental clock
	extra []*synth.PhaseClock
	mults []float64 // frequency multipliers for extra clocks, if any
	out   *synth.Wire
	voice *synth.Voice
	level float64
	gen   func() float64
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

	w.clock = v.Synth().Instant().AddPhaseClock()
	w.clock.SetFrequency(v.NoteFreq())

	switch shape {
	case "sine":
		w.gen = func() float64 { return sineGen(w.clock.Phase()) }
	case "saw":
		w.gen = func() float64 { return sawGen(w.clock.Phase()) }
	case "square":
		w.gen = func() float64 { return squareGen(w.clock.Phase()) }
	case "ramp-up":
		w.gen = func() float64 { return rampUpGen(w.clock.Phase()) }
	case "ramp-down":
		w.gen = func() float64 { return rampDownGen(w.clock.Phase()) }
	case "harmonic":
		params, err := parseWaveParams(spec)
		if err != nil {
			return err
		}
		w.gen = makeHarmonicGen(w, params)
	case "anharmonic":
		params, err := parseWaveParams(spec)
		if err != nil {
			return err
		}
		w.gen = makeAnharmonicGen(w, params)
	case "noise":
		w.gen = makeNoiseGen()
	default:
		return fmt.Errorf("wave: shape %q not yet implemented", shape)
	}

	w.level = waveDefaultLevel
	if ls, ok := numeric(spec["level-scale"]); ok {
		w.level *= ls
	}

	w.out = v.NewWire(w.sample)
	return nil
}

func (w *Wave) Output() *synth.Wire { return w.out }

// retunes the fundamental and any extra clocks to track the new note.
func (w *Wave) retune(midiKey float64) {
	base := 440 * math.Pow(2, (midiKey-69)/12)
	w.clock.SetFrequency(base)
	w.clock.ResetPhase()
	for i, c := range w.extra {
		c.SetFrequency(base * w.mults[i])
		c.ResetPhase()
	}
}

// OnMidi resets the oscillator phase and re-pitches on note-on.
func (w *Wave) OnMidi(m synth.MidiMsg) {
	if m.IsNoteOn() {
		w.retune(float64(m.Data1))
	}
}

func (w *Wave) sample() float64 {
	return w.gen() * w.level
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

// parseWaveParams reads either `preset:` (named preset) or `waves:`
// (list of mult/divisor tokens, possibly grouped per line) into a flat
// even-length []float64. Mirrors HarmonicWaveGen.configure and the
// AnharmonicWaveGen variant of same.
func parseWaveParams(spec component.Spec) ([]float64, error) {
	if p, ok := spec["preset"].(string); ok {
		if params, ok := harmonicPresets[p]; ok {
			return params, nil
		}
		return nil, fmt.Errorf("wave: unknown preset %q", p)
	}
	raw, ok := spec["waves"]
	if !ok {
		return nil, fmt.Errorf("wave: this shape needs preset: or waves:")
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("wave: waves must be a list")
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
		return nil, fmt.Errorf("wave: waves must be pairs of (mult, divisor), got %d tokens", len(tokens))
	}
	params := make([]float64, len(tokens))
	for i, t := range tokens {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("wave: param %q: %w", t, err)
		}
		params[i] = f
	}
	return params, nil
}

// makeHarmonicGen sums sin(2*pi*phase*mult)/divisor over each pair,
// using the fundamental phase clock. Non-integer multipliers fold back
// to phase 0 with the base, producing the buzzy phase-alignment noted
// in HarmonicWaveGen's doc.
func makeHarmonicGen(w *Wave, params []float64) func() float64 {
	return func() float64 {
		phase := w.clock.Phase()
		var sum float64
		for i := 0; i+1 < len(params); i += 2 {
			sum += math.Sin(2*math.Pi*phase*params[i]) / params[i+1]
		}
		return sum
	}
}

// makeAnharmonicGen separates the input into integer-mult (harmonic,
// summed against the fundamental phase clock) and non-integer-mult
// (anharmonic, each with its own clock running at base*mult so phase
// alignment never collapses).
func makeAnharmonicGen(w *Wave, params []float64) func() float64 {
	var harm, anharm []float64
	for i := 0; i+1 < len(params); i += 2 {
		if math.Mod(params[i], 1.0) == 0 {
			harm = append(harm, params[i], params[i+1])
		} else {
			anharm = append(anharm, params[i], params[i+1])
		}
	}
	for i := 0; i+1 < len(anharm); i += 2 {
		c := w.voice.Synth().Instant().AddPhaseClock()
		c.SetFrequency(w.clock.Frequency() * anharm[i])
		w.extra = append(w.extra, c)
		w.mults = append(w.mults, anharm[i])
	}
	return func() float64 {
		var sum float64
		phase := w.clock.Phase()
		for i := 0; i+1 < len(harm); i += 2 {
			sum += math.Sin(2*math.Pi*phase*harm[i]) / harm[i+1]
		}
		for i := 0; i+1 < len(anharm); i += 2 {
			sum += math.Sin(2*math.Pi*w.extra[i/2].Phase()) / anharm[i+1]
		}
		return sum
	}
}

// makeNoiseGen mimics NoiseWaveGen: latches at two hold-lengths (1 and
// 23 samples), averages, then low-passes via lastValue += diff/20. The
// random seed is unspecified per run, matching Java's `new Random()`.
func makeNoiseGen() func() float64 {
	rng := rand.New(rand.NewSource(rand.Int63()))
	holds := []int{1, 23}
	latch := make([]float64, len(holds))
	var last float64
	var n int64
	return func() float64 {
		n++
		for i, h := range holds {
			if n%int64(h) == 0 {
				latch[i] = rng.Float64()*2 - 1
			}
		}
		var s float64
		for _, v := range latch {
			s += v
		}
		cur := s / float64(len(latch))
		last += (cur - last) / 20
		return last * 3
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
