// Package filter implements the IIR filter component. Mirrors
// ondes.synth.filter.iir.IIRFilter: a/b coefficient pairs from the
// hard-coded Java IIRSpecLib, applied as a direct-form-I difference
// equation with circular x/y buffers.
//
// Channel-context filters (one filter shared across all voices on a
// channel, as bell.yaml uses) are not yet supported - this is per-voice
// only.
package filter

import (
	"fmt"
	"math"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("filter", func() component.Component { return &Filter{} })
}

type Filter struct {
	voice      *synth.Voice
	out        *synth.Wire
	inputs     []*synth.Wire
	a, b       []float64
	x, y       []float64
	x0, y0     int
	levelScale float64
	sampleRate float64

	// sinc-only state: a running-average (box) low-pass. The buffer
	// spans one period of `sincFreq` (sampleRate/freq samples); its mean
	// is the output. With sincMidi set, note-on retunes freq to the
	// played note. freq==0 means pass-through (buffer nil).
	sincFreq    float64
	sincMidi    bool
	sincBuf     []float64
	sincBufLen  int
	sincBufIdx  int
	sincSum     float64
	sincFilling bool // true until the buffer has filled once (outputs 0 meanwhile)

	// biquad-only state
	bqFreq, bqQ             float64
	bqFreqOffset, bqQOffset float64
	bqFreqAmp, bqFreqRange  float64
	bqQAmp, bqQRange        float64
	modFreq, modQ           bool
	freqInputs, qInputs     []*synth.Wire
}

func (f *Filter) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	f.voice = v
	f.levelScale = 1
	f.sampleRate = float64(v.Synth().SampleRate())

	shape, _ := spec["shape"].(string)
	switch shape {
	case "", "sinc":
		// Java's FilterMaker defaults a missing shape to "sinc".
		if fr, ok := numeric(spec["freq"]); ok {
			f.sincFreq = fr
		}
		if _, ok := spec["midi"]; ok {
			f.sincMidi = true
		}
		f.sincReset()
		f.out = v.NewWire(f.computeSinc)
	case "iir":
		key, _ := spec["key"].(string)
		if key == "" {
			return fmt.Errorf("filter: key required for iir")
		}
		coef, ok := iirSpec(key)
		if !ok {
			return fmt.Errorf("filter: unknown iir key %q", key)
		}
		f.a = coef.a
		f.b = coef.b
		f.x = make([]float64, len(f.a))
		f.y = make([]float64, len(f.b))
		f.out = v.NewWire(f.compute)
	case "biquad":
		f.a = make([]float64, 3)
		f.b = make([]float64, 3)
		f.x = make([]float64, 3)
		f.y = make([]float64, 3)
		if fr, ok := numeric(spec["freq"]); ok {
			f.bqFreq = fr
		}
		if q, ok := numeric(spec["Q"]); ok {
			f.bqQ = q
		}
		if mp, ok := spec["input-freq"].(map[string]any); ok {
			if amp, ok := numeric(mp["amp"]); ok {
				if rg, ok := numeric(mp["range"]); ok {
					f.bqFreqAmp = amp
					f.bqFreqRange = rg
					f.modFreq = true
				}
			}
		}
		if mp, ok := spec["input-Q"].(map[string]any); ok {
			if amp, ok := numeric(mp["amp"]); ok {
				if rg, ok := numeric(mp["range"]); ok {
					f.bqQAmp = amp
					f.bqQRange = rg
					f.modQ = true
				}
			}
		}
		f.bqSetCoefficients(f.bqFreq, f.bqQ)
		f.out = v.NewWire(f.computeBiquad)
	default:
		return fmt.Errorf("filter: shape %q not supported", shape)
	}

	if ls, ok := numeric(spec["level-scale"]); ok {
		f.levelScale = ls
	}

	return nil
}

func (f *Filter) Output() *synth.Wire { return f.out }

// OnMidi retunes a sinc filter to the played note (Java's SincFilter
// .noteON). Only sinc filters declared with `midi: note-on` respond;
// iir/biquad use fixed coefficients and ignore notes.
func (f *Filter) OnMidi(m synth.MidiMsg) {
	if f.sincMidi && m.IsNoteOn() {
		f.sincFreq = 440 * math.Pow(2, (float64(m.Data1)-69)/12)
		f.sincReset()
	}
}

// sincReset (re)allocates the running-average buffer for the current
// frequency. freq==0 leaves the buffer nil, making the filter a
// pass-through (matching Java's arraySize()==0 case).
func (f *Filter) sincReset() {
	if f.sincFreq > 0 {
		f.sincBufLen = int(f.sampleRate / f.sincFreq)
		f.sincBuf = make([]float64, f.sincBufLen)
		f.sincBufIdx = 0
		f.sincSum = 0
		f.sincFilling = true
	} else {
		f.sincBuf = nil
	}
}

// sincNextAverage feeds n into the circular buffer and returns the
// running mean. It outputs 0 until the buffer has filled once, which
// avoids a startup transient (matching Java's `first` flag).
func (f *Filter) sincNextAverage(n float64) float64 {
	if f.sincBuf == nil {
		return n
	}
	if !f.sincFilling {
		f.sincSum -= f.sincBuf[f.sincBufIdx]
	}
	if f.sincBufIdx == f.sincBufLen-1 {
		f.sincFilling = false
	}
	f.sincSum += n
	f.sincBuf[f.sincBufIdx] = n
	f.sincBufIdx = (f.sincBufIdx + 1) % f.sincBufLen
	if f.sincFilling {
		return 0
	}
	return f.sincSum / float64(f.sincBufLen)
}

func (f *Filter) computeSinc() float64 {
	var x float64
	for _, w := range f.inputs {
		x += w.Get()
	}
	return f.sincNextAverage(f.levelScale * x)
}

func (f *Filter) AddInput(sel string, w *synth.Wire) {
	switch sel {
	case "freq":
		f.freqInputs = append(f.freqInputs, w)
	case "Q":
		f.qInputs = append(f.qInputs, w)
	default:
		f.inputs = append(f.inputs, w)
	}
}

func (f *Filter) compute() float64 {
	var x float64
	for _, w := range f.inputs {
		x += w.Get()
	}
	f.x[f.x0] = x
	var sigma float64
	for i := 0; i < len(f.b); i++ {
		sigma += f.b[i] * f.x[(len(f.x)+f.x0-i)%len(f.x)]
	}
	f.x0 = (f.x0 + 1) % len(f.x)

	for i := 1; i < len(f.a); i++ {
		sigma -= f.a[i] * f.y[(len(f.y)+f.y0-i)%len(f.y)]
	}
	f.y[f.y0] = sigma
	f.y0 = (f.y0 + 1) % len(f.y)
	return sigma * f.levelScale
}

// bqSetCoefficients - RBJ-style lowpass with Java's custom alpha
// (sin(omega) * sinh(0.5/Q) rather than the canonical
// sinh(ln(2)/2 * BW * omega/sin(omega))).
func (f *Filter) bqSetCoefficients(freq, q float64) {
	if q <= 0 {
		q = 0.5
	}
	omega := 2 * math.Pi * (freq / f.sampleRate)
	alpha := math.Sin(omega) * math.Sinh(0.5/q)

	f.a[0] = 1.0 + alpha
	f.a[1] = -2.0 * math.Cos(omega)
	f.a[2] = 1.0 - alpha

	f.b[1] = 1 - math.Cos(omega)
	f.b[0] = 0.5 * f.b[1]
	f.b[2] = f.b[0]

	if f.a[0] <= 0 || f.a[2] >= 1.0 || (1+f.a[2]) <= math.Abs(f.a[1]) {
		for i := range f.a {
			f.a[i] = 1
			f.b[i] = 1
		}
		return
	}
	a0r := 1.0 / f.a[0]
	f.a[0] = 1
	f.a[1] *= a0r
	f.a[2] *= a0r
	for i := range f.b {
		f.b[i] *= a0r
	}
}

func (f *Filter) computeBiquad() float64 {
	dirty := false
	if f.modFreq {
		var s float64
		for _, w := range f.freqInputs {
			s += w.Get()
		}
		// Java: range * inp / amp, in int units. Our float inp is
		// scaled by 1/32767, so multiply back.
		newOff := f.bqFreqRange * s * 32767.0 / f.bqFreqAmp
		if newOff != f.bqFreqOffset {
			f.bqFreqOffset = newOff
			dirty = true
		}
	}
	if f.modQ {
		var s float64
		for _, w := range f.qInputs {
			s += w.Get()
		}
		newOff := f.bqQRange * s * 32767.0 / f.bqQAmp
		if newOff != f.bqQOffset {
			f.bqQOffset = newOff
			dirty = true
		}
	}
	if dirty {
		f.bqSetCoefficients(f.bqFreq+f.bqFreqOffset, f.bqQ+f.bqQOffset)
	}

	var x float64
	for _, w := range f.inputs {
		x += w.Get()
	}
	// Direct form 1, sliding via index 0/1/2 (matches Java's array layout).
	f.x[0] = x
	y0 := f.b[0]*f.x[0] + f.b[1]*f.x[1] + f.b[2]*f.x[2] - f.a[1]*f.y[1] - f.a[2]*f.y[2]
	f.x[2] = f.x[1]
	f.x[1] = f.x[0]
	f.y[2] = f.y[1]
	f.y[1] = y0
	f.y[0] = y0
	return y0 * f.levelScale
}

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
