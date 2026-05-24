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
}

func (f *Filter) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	f.voice = v
	f.levelScale = 1

	shape, _ := spec["shape"].(string)
	if shape != "iir" {
		return fmt.Errorf("filter: shape %q not supported (only iir)", shape)
	}
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

	if ls, ok := numeric(spec["level-scale"]); ok {
		f.levelScale = ls
	}

	f.out = v.NewWire(f.compute)
	return nil
}

func (f *Filter) Output() *synth.Wire { return f.out }

func (f *Filter) AddInput(_ string, w *synth.Wire) { f.inputs = append(f.inputs, w) }

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
