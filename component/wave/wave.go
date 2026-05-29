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

// genFactory builds a shape's per-sample generator from the wave and its
// spec, returning an error for bad config. It is the wave-shape analogue
// of component.Factory (and of Java's WaveMaker name->class registry).
type genFactory func(w *Wave, spec component.Spec) (func() float64, error)

// waveGens maps a YAML `shape` value to its generator factory. Shapes
// register here from init() instead of living in a Configure switch, so
// adding a shape is a self-contained registration (and could move to its
// own file) rather than an edit to the dispatch site.
var waveGens = map[string]genFactory{}

func registerShape(name string, f genFactory) {
	if _, dup := waveGens[name]; dup {
		panic("wave: duplicate shape registration for " + name)
	}
	waveGens[name] = f
}

// registerPhaseShape registers a simple periodic shape that is fully
// defined by a phase->amplitude function over the fundamental clock.
func registerPhaseShape(name string, fn func(phase float64) float64) {
	registerShape(name, func(w *Wave, _ component.Spec) (func() float64, error) {
		return func() float64 { return fn(w.clock.Phase()) }, nil
	})
}

func init() {
	registerPhaseShape("sine", sineGen)
	registerPhaseShape("saw", sawGen)
	registerPhaseShape("square", squareGen)
	registerPhaseShape("ramp-up", rampUpGen)
	registerPhaseShape("ramp-down", rampDownGen)

	registerShape("harmonic", func(w *Wave, spec component.Spec) (func() float64, error) {
		params, err := parseWaveParams(spec)
		if err != nil {
			return nil, err
		}
		return makeHarmonicGen(w, params), nil
	})
	registerShape("anharmonic", func(w *Wave, spec component.Spec) (func() float64, error) {
		params, err := parseWaveParams(spec)
		if err != nil {
			return nil, err
		}
		return makeAnharmonicGen(w, params), nil
	})
	registerShape("noise", func(_ *Wave, _ component.Spec) (func() float64, error) {
		return makeNoiseGen(), nil
	})
	registerShape("pink", func(_ *Wave, _ component.Spec) (func() float64, error) {
		return makePinkGen(), nil
	})
	registerShape("pwm", func(w *Wave, spec component.Spec) (func() float64, error) {
		return makePWMGen(w, spec), nil
	})
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
	velBase     float64 // velocity-base/100, default 0
	velAmount   float64 // velocity-amount/100, default 1
	velGain     float64 // current note's velocity multiplier (1 until first note-on)
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
	w.velAmount = 1
	w.velGain = 1
	if vb, ok := numeric(spec["velocity-base"]); ok {
		w.velBase = vb / 100
	}
	if va, ok := numeric(spec["velocity-amount"]); ok {
		w.velAmount = va / 100
	}
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

	factory, ok := waveGens[shape]
	if !ok {
		return fmt.Errorf("wave: shape %q not yet implemented", shape)
	}
	gen, err := factory(w, spec)
	if err != nil {
		return err
	}
	w.gen = gen

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
// (fixedFreq) keep their running phase across notes and ignore velocity
// (in Java they would be channel-context components that never receive
// per-voice note-ons).
func (w *Wave) OnMidi(m synth.MidiMsg) {
	if m.IsNoteOn() && !w.fixedFreq {
		w.retune(float64(m.Data1))
		w.velGain = w.velBase + w.velAmount*float64(m.Data2)/128
		if w.velGain > 1 {
			w.velGain = 1
		}
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
	return v * w.level * w.velGain
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

// makeNoiseGen and makePinkGen mirror NoiseWaveGen and PinkNoiseGen,
// which are identical except for how the output chases the latch mean:
// the only difference is the `chase` rule passed to makeColoredNoise.
func makeNoiseGen() func() float64 {
	// Low-pass toward the mean by a fixed fraction.
	return makeColoredNoise(func(last, cur float64) float64 {
		return last + (cur-last)/20
	})
}

func makePinkGen() func() float64 {
	// Chase the mean by a minimum step rather than a fraction. The step
	// is max(|diff|, 3)*sign(diff) in Java's int units (ampBase 1024), so
	// the minimum normalizes to 3/1024; large diffs snap fully to target.
	// The enforced minimum keeps the signal perpetually jittery - the
	// characteristic pink "static".
	const minStep = 3.0 / 1024.0
	return makeColoredNoise(func(last, cur float64) float64 {
		switch diff := cur - last; {
		case diff > 0:
			return last + math.Max(diff, minStep)
		case diff < 0:
			return last - math.Max(-diff, minStep)
		default:
			return last
		}
	})
}

// makeColoredNoise is the shared noise machinery: latch fresh random
// values at two hold-lengths (1 and 23 samples), average them, then
// advance the output toward that mean by the given chase rule. The
// random seed is unspecified per run, matching Java's `new Random()`.
func makeColoredNoise(chase func(last, cur float64) float64) func() float64 {
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
		last = chase(last, s/float64(len(latch)))
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
