// Package echo implements the `echo` component: a feedback delay line.
// Mirrors ondes.synth.effect.Echo.
//
//	y[n] = x[n] + amount * tape[t0]
//	tape[(t0+offset) % len] = y[n]
//
// where `amount` is the feedback fraction (config `amount:` percent,
// divided by 100), `offset` is the delay in samples (config `time:` ms),
// and the tape is a circular buffer that length samples long.
//
// The feedback amount can be modulated by wiring a source to the
// `amount` pin (e.g. `out: echo.amount`); the delta is added to the
// base before the /100 scale, matching Java's ModParam. Delay-*time*
// modulation is not ported: Java fades the tape out to avoid clicks when
// the delay length changes, and no patch in the suite uses it.
package echo

import (
	"math"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("echo", func() component.Component { return &Echo{} })
}

type Echo struct {
	inputs     []*synth.Wire
	amountMod  []*synth.Wire
	out        *synth.Wire
	tape       []float64
	t0         int
	offset     int
	amountBase float64 // feedback fraction before modulation (config/100)
	levelScale float64
}

func (e *Echo) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	e.levelScale = 1
	if ls, ok := numeric(spec["level-scale"]); ok {
		e.levelScale = ls
	}

	amount := 0.0
	if a, ok := numeric(spec["amount"]); ok {
		amount = a
	}
	e.amountBase = amount / 100.0

	timeMs := 1000.0
	if t, ok := numeric(spec["time"]); ok {
		timeMs = t
	}
	sr := float64(v.Synth().SampleRate())
	n := int(math.Ceil(timeMs / 1000.0 * sr))
	if n < 1 {
		n = 1
	}
	e.tape = make([]float64, n)
	e.offset = n

	e.out = v.NewWire(e.compute)
	return nil
}

func (e *Echo) Output() *synth.Wire { return e.out }

func (e *Echo) AddInput(sel string, w *synth.Wire) {
	if sel == "amount" {
		e.amountMod = append(e.amountMod, w)
		return
	}
	e.inputs = append(e.inputs, w)
}

func (e *Echo) compute() float64 {
	amount := e.amountBase
	if len(e.amountMod) > 0 {
		var delta float64
		for _, w := range e.amountMod {
			delta += w.Get()
		}
		// Java adds the modulation delta (in percent units) to the base
		// before the /100 scale; our float inputs are 1.0 == 32767 units.
		amount += delta * 32767.0 / 100.0
	}

	var x0 float64
	for _, w := range e.inputs {
		x0 += w.Get()
	}
	y0 := x0 + e.tape[e.t0]*amount
	e.tape[(e.t0+e.offset)%len(e.tape)] = y0
	e.t0 = (e.t0 + 1) % len(e.tape)
	return e.levelScale * y0
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
