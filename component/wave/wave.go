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
	shape       string
	clock       *synth.PhaseClock // fundamental clock
	extra       []*synth.PhaseClock
	mults       []float64 // frequency multipliers for extra clocks, if any
	out         *synth.Wire
	voice       *synth.Voice
	level       float64
	freqMul     float64 // applied to NoteFreq: 2^(offset/12) * 2^(detune/1200)
	baseFreq    float64 // unmodulated frequency, set in retune
	fixedFreq   bool    // true if `freq:` is set (LFO), skip note retune
	unsigned    bool    // true if signed: false (unipolar output [0, 2*level])
	namedInputs map[string][]*synth.Wire

	// Log-frequency modulation (input-log). Inactive when logModExp == 0.
	logAmpInv float64 // 1 / (input-log amp, in our float units)
	logModExp float64 // semitones / 12, so 2^(ratio * logModExp) covers ±semitones

	gen func() float64
}

// waveDefaultLevel is the empirical scale that makes a bare sine patch
// produce roughly the same RMS as Java's reference (peak ~800 against
// the int16 full-scale of 32767 ~ 0.024). Until we model the Java
// WaveGen's level handling precisely, this gets us within tolerance.
const waveDefaultLevel = 0.025

func (w *Wave) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	w.voice = v
	w.namedInputs = map[string][]*synth.Wire{}
	shape, _ := spec["shape"].(string)
	if shape == "" {
		return fmt.Errorf("wave: missing shape")
	}
	w.shape = shape

	w.freqMul = 1
	if off, ok := numeric(spec["offset"]); ok {
		w.freqMul *= math.Pow(2, off/12)
	}
	if det, ok := numeric(spec["detune"]); ok {
		w.freqMul *= math.Pow(2, det/1200)
	}
	if signed, ok := spec["signed"].(bool); ok && !signed {
		w.unsigned = true
	}

	w.clock = v.Synth().Instant().AddPhaseClock()
	if f, ok := numeric(spec["freq"]); ok {
		w.fixedFreq = true
		w.baseFreq = f * w.freqMul
	} else {
		w.baseFreq = v.NoteFreq() * w.freqMul
	}
	w.clock.SetFrequency(w.baseFreq)

	if il, ok := spec["input-log"].(map[string]any); ok {
		amp, _ := numeric(il["amp"])
		semis, _ := numeric(il["semitones"])
		if amp > 0 {
			w.logAmpInv = 32767.0 / amp
			w.logModExp = semis / 12.0
		}
	}

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
	case "pwm":
		w.gen = makePWMGen(w, spec)
	default:
		return fmt.Errorf("wave: shape %q not yet implemented", shape)
	}

	if lo, ok := numeric(spec["level-override"]); ok {
		// Java treats level-override as raw int16 amplitude; map to our float scale.
		w.level = lo / 32767.0
	} else {
		w.level = waveDefaultLevel
	}
	if ls, ok := numeric(spec["level-scale"]); ok {
		w.level *= ls
	}

	w.out = v.NewWire(w.sample)
	return nil
}

// AddInput attaches a modulation source on a named pin (e.g. "pwm").
// PWM-type waves consume these; basic shapes ignore them.
func (w *Wave) AddInput(selectName string, src *synth.Wire) {
	if selectName == "" {
		selectName = "main"
	}
	w.namedInputs[selectName] = append(w.namedInputs[selectName], src)
}

// namedInputSum sums all wires attached to the given pin.
func (w *Wave) namedInputSum(pin string) float64 {
	var s float64
	for _, in := range w.namedInputs[pin] {
		s += in.Get()
	}
	return s
}

// modFreq applies log frequency modulation to the fundamental clock
// (and any anharmonic-partial clocks). One-sample latency vs Java's
// implementation - same as Java, since the phase clock has already
// ticked for this sample before we get here.
func (w *Wave) modFreq() {
	if w.logModExp == 0 {
		return
	}
	logInp := w.namedInputSum("log") * w.logAmpInv * w.logModExp
	freq := w.baseFreq * math.Pow(2, logInp)
	w.clock.SetFrequency(freq)
	for i, c := range w.extra {
		c.SetFrequency(freq * w.mults[i])
	}
}

func (w *Wave) Output() *synth.Wire { return w.out }

// retunes the fundamental and any extra clocks to track the new note,
// scaled by the static offset/detune multiplier.
func (w *Wave) retune(midiKey float64) {
	w.baseFreq = 440 * math.Pow(2, (midiKey-69)/12) * w.freqMul
	w.clock.SetFrequency(w.baseFreq)
	w.clock.ResetPhase()
	for i, c := range w.extra {
		c.SetFrequency(w.baseFreq * w.mults[i])
		c.ResetPhase()
	}
}

// OnMidi resets the oscillator phase and re-pitches on note-on. LFOs
// (fixedFreq) keep their running phase across notes.
func (w *Wave) OnMidi(m synth.MidiMsg) {
	if m.IsNoteOn() && !w.fixedFreq {
		w.retune(float64(m.Data1))
	}
}

func (w *Wave) sample() float64 {
	w.modFreq()
	v := w.gen()
	if w.unsigned {
		// Java's WaveGen subclasses build signed [-amp,+amp] output by
		// doubling a natural [0, amp] form (ramp-up/down) or by
		// centering a [-amp, +amp] form (saw, square, sine). Unsigned
		// reverses to: ramp-up/down → [0, amp]; saw/square → [0, 2*amp].
		switch w.shape {
		case "ramp-up", "ramp-down":
			v = (v + 1) * 0.5
		default:
			v += 1
		}
	}
	return v * w.level
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

// makePWMGen builds a pulse-width-modulated square. mod-percent is the
// peak swing of the duty cycle (0-100, but in Java's code the
// expression is `dutyCycle + (modPercent/200) * mod` so 100% mod-percent
// swings the duty by +/- 0.5). input-amp normalizes the modulation
// input back to [-1, +1] (it's the amplitude the upstream LFO is
// expected to produce).
func makePWMGen(w *Wave, spec component.Spec) func() float64 {
	dutyCycle := 0.5
	var modPercent, inputAmpInv float64
	if v, ok := numeric(spec["mod-percent"]); ok {
		if v >= 0 && v <= 100 {
			modPercent = v
		}
	}
	if v, ok := numeric(spec["input-amp"]); ok && v != 0 {
		// input-amp is in Java-int units; our LFO outputs float64 in
		// [-amp/32767, +amp/32767] when level-override is set, so we
		// need the same scale on the input side: the upstream's
		// my-float matches Java's int/32767, so inputAmpInv must be
		// in the same units.
		inputAmpInv = 32767.0 / v
	}
	return func() float64 {
		mod := w.namedInputSum("pwm") * inputAmpInv
		duty := dutyCycle + (modPercent/200.0)*mod
		if w.clock.Phase() > duty {
			return 1
		}
		return -1
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
