// Package balancer implements the `balancer` component: a controller-
// driven crossfade between its `left` and `right` inputs. Mirrors
// ondes.synth.wire.Balancer.
//
//	lScale = (ctrl/ctrlInputAmp + 1) / 2     // 0..1 as ctrl sweeps -amp..+amp
//	out    = (lScale*left + (1-lScale)*right) * level-scale
//
// With no `ctrl` input connected the sum is 0, giving lScale 0.5 - an
// even blend of left and right.
//
// Scale note: ctrlInputAmp is configured in Java-int units; our ctrl
// wire is in float units (Java-int / 32767), so we divide by
// ctrlInputAmp/32767 to recover the int-domain ratio. The `initial-value`
// config is parsed for compatibility but, as in Java, never affects the
// output (ctrl comes entirely from the wired input).
package balancer

import (
	"ondes/component"
	"ondes/synth"
)

const intScale = 32767.0

func init() {
	component.Register("balancer", func() component.Component { return &Balancer{} })
}

type Balancer struct {
	out         *synth.Wire
	namedInputs map[string][]*synth.Wire

	scale           float64
	ctrlInputAmpInv float64 // intScale / ctrlInputAmp
}

func (b *Balancer) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	b.namedInputs = map[string][]*synth.Wire{}
	b.scale = 1
	ctrlInputAmp := 1000.0 // Java default

	if ls, ok := numeric(spec["level-scale"]); ok {
		b.scale = ls
	}
	// input-ctrl: { amp: <int>, initial-value: <float> }. Both keys must
	// be present for the override to take effect (matching getInAmpPair).
	if amp, _, ok := inAmpPair(spec, "input-ctrl", "initial-value"); ok {
		ctrlInputAmp = amp
	}
	b.ctrlInputAmpInv = intScale / ctrlInputAmp

	b.out = v.NewWire(b.compute)
	return nil
}

func (b *Balancer) Output() *synth.Wire { return b.out }

func (b *Balancer) AddInput(selectName string, w *synth.Wire) {
	if selectName == "" {
		selectName = "main"
	}
	b.namedInputs[selectName] = append(b.namedInputs[selectName], w)
}

func (b *Balancer) namedInputSum(pin string) float64 {
	var sum float64
	for _, w := range b.namedInputs[pin] {
		sum += w.Get()
	}
	return sum
}

func (b *Balancer) compute() float64 {
	l := b.namedInputSum("left")
	r := b.namedInputSum("right")
	ctrl := b.namedInputSum("ctrl")

	lScale := (ctrl*b.ctrlInputAmpInv + 1) / 2
	return (lScale*l + (1-lScale)*r) * b.scale
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

func inAmpPair(spec component.Spec, key, prop string) (amp, propVal float64, ok bool) {
	m, isMap := spec[key].(map[string]any)
	if !isMap {
		return 0, 0, false
	}
	amp, aok := numeric(m["amp"])
	propVal, pok := numeric(m[prop])
	if !aok || !pok {
		return 0, 0, false
	}
	return amp, propVal, true
}
