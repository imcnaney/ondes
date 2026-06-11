// Package mix implements the `mix` and `dynamic-mix` components: the
// simplest mixers, summing every input wire and multiplying by a single
// level scale. Mirrors ondes.synth.wire.Junction and DynamicJunction.
//
// `dynamic-mix` additionally tracks MIDI controller 7 (channel volume),
// setting level-scale = value/128. The regression MIDI carries no CC
// data, so for those fixtures a dynamic-mix behaves identically to a
// plain mix (level-scale 1).
package mix

import (
	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("mix", func() component.Component { return &Mix{} })
	component.Register("dynamic-mix", func() component.Component { return &Mix{dynamic: true} })
}

// Mix sums its inputs and scales the result. When dynamic, it responds
// to MIDI volume (CC 7) like Java's DynamicJunction.
type Mix struct {
	out        *synth.Wire
	inputs     []*synth.Wire
	levelScale float64
	dynamic    bool
}

func (m *Mix) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	m.levelScale = 1
	if ls, ok := numeric(spec["level-scale"]); ok {
		m.levelScale = ls
	}
	m.out = v.NewWire(m.compute)
	return nil
}

func (m *Mix) Output() *synth.Wire { return m.out }

func (m *Mix) AddInput(_ string, w *synth.Wire) { m.inputs = append(m.inputs, w) }

func (m *Mix) compute() float64 {
	var s float64
	for _, w := range m.inputs {
		s += w.Get()
	}
	return s * m.levelScale
}

// OnMidi implements the MIDI volume response of a dynamic-mix. Plain
// mixes ignore it.
func (m *Mix) OnMidi(msg synth.MidiMsg) {
	if !m.dynamic {
		return
	}
	// Controller change, CC 7 (channel volume).
	if msg.Status&0xF0 == 0xB0 && msg.Data1 == 7 {
		m.levelScale = float64(msg.Data2) / 128.0
	}
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
