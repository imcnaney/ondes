// Package opamp implements the op-amp component: multiplies its inputs
// together, then scales the product. Used as a VCA and as a ring
// modulator (when two oscillators are multiplied).
//
// Java's OpAmp works in integer space where each oscillator peaks
// around ampBase=1024, so the product of two oscs is in the 1e6 range
// and a level-scale of 0.01 brings that back to useful audio. Our
// internal samples are float64 [-1, +1], scaled so the bare-sine peak
// matches Java's ~720/32767. To keep level-scale values from existing
// patches meaningful, we compensate by the same int-scale factor per
// extra input.
package opamp

import (
	"math"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("op-amp", func() component.Component { return &OpAmp{} })
}

// engineIntScale is the ratio between Java's ampBase (1024) and our
// matching float amplitude (waveDefaultLevel = 0.025). Each multiplied
// input contributes one factor; one input is "free" because the
// downstream chain still expects float64 in [-1, +1].
const engineIntScale = 1024.0 / 0.025

type OpAmp struct {
	voice  *synth.Voice
	out    *synth.Wire
	inputs []*synth.Wire
	scale  float64
	corr   float64 // engineIntScale ^ (numInputs - 1), computed lazily
}

func (o *OpAmp) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	o.voice = v
	o.scale = 1
	if ls, ok := numeric(spec["level-scale"]); ok {
		o.scale = ls
	}
	o.out = v.NewWire(o.compute)
	return nil
}

func (o *OpAmp) Output() *synth.Wire { return o.out }

func (o *OpAmp) AddInput(_ string, w *synth.Wire) { o.inputs = append(o.inputs, w) }

func (o *OpAmp) compute() float64 {
	if len(o.inputs) == 0 {
		return 0
	}
	if o.corr == 0 {
		o.corr = math.Pow(engineIntScale, float64(len(o.inputs)-1))
	}
	prod := 1.0
	for _, w := range o.inputs {
		prod *= w.Get()
	}
	return prod * o.scale * o.corr
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
