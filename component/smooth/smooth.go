// Package smooth implements the `smooth` component: a non-linear
// smoother that fattens the bass of whatever feeds it. Mirrors
// ondes.synth.filter.Smooth.
//
// Each sample the output y0 approaches the input, moving faster the
// farther away it is: y0 += signum(delta)*k + delta*k. The proportional
// term (delta*k) gives an exponential approach; the constant term
// (signum*k) is a linear floor that guarantees y0 reaches the target.
// The result is a concave/convex curve, steepest at the start - which
// emphasizes low frequencies.
//
// Scale note: the Java engine works in integer sample units (~+/-1000
// per oscillator); ours are float64 (Java-int / 32767). The
// proportional term scales with the signal and ports unchanged, but the
// constant signum step is an absolute amplitude and must be divided by
// 32767 to stay the same physical step in our units. compScale and
// level-scale are dimensionless and apply directly.
package smooth

import (
	"fmt"
	"math"
	"os"

	"ondes/component"
	"ondes/synth"
)

// intScale converts a Java-int amplitude to our float sample units.
const intScale = 32767.0

func init() {
	component.Register("smooth", func() component.Component { return &Smooth{} })
}

type Smooth struct {
	out         *synth.Wire
	inputs      []*synth.Wire // default ("main") signal inputs
	namedInputs map[string][]*synth.Wire

	y0         float64
	levelScale float64
	compScale  float64
	k, kInv    float64

	amount        float64
	amtInputAmp   float64
	amtInputRange float64
	modAmt        bool
}

func (s *Smooth) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	s.namedInputs = map[string][]*synth.Wire{}
	s.levelScale = 1
	s.amount = 1

	if a, ok := numeric(spec["amount"]); ok {
		s.amount = math.Abs(a)
	}
	if s.amount < 1 {
		fmt.Fprintln(os.Stderr, "Smooth amount cannot be <1. Setting to 1")
		s.amount = 1
	}
	s.setK(0)

	if ls, ok := numeric(spec["level-scale"]); ok {
		if ls < 0 || ls > 11 {
			fmt.Fprintln(os.Stderr, "smooth: 'level-scale' must be between 0 and 11")
		} else {
			s.levelScale = ls
		}
	}

	// input-amount: { amp: <int>, range: <float> } enables modulation of
	// the smoothing amount via the "range" pin. Both keys are required.
	if amp, rng, ok := inAmpPair(spec, "input-amount", "range"); ok {
		s.amtInputAmp = amp
		s.amtInputRange = rng
		s.modAmt = true
	}

	s.out = v.NewWire(s.compute)
	return nil
}

func (s *Smooth) Output() *synth.Wire { return s.out }

func (s *Smooth) AddInput(selectName string, w *synth.Wire) {
	if selectName == "" || selectName == "main" {
		s.inputs = append(s.inputs, w)
		return
	}
	s.namedInputs[selectName] = append(s.namedInputs[selectName], w)
}

// setK recomputes the smoothing coefficients. inp is the (dimensionless)
// modulation amount; larger inp -> smaller k -> heavier smoothing, with
// compScale rising to compensate for the amplitude the smoothing eats.
func (s *Smooth) setK(inp float64) {
	s.kInv = s.amount + inp*inp
	s.k = 1.0 / s.kInv
	s.compScale = 1 + s.kInv/2.5 // empirically the best amplitude match
}

// modAmt aligns k with the "range" modulation input. The Java math is
// amtInputRange * (rangeInput / amtInputAmp) where rangeInput is in
// int units; our range wire is in float units, so multiply by intScale
// to recover the int-domain ratio.
func (s *Smooth) modAmtUpdate() {
	rangeIn := s.namedInputSum("range") * intScale
	s.setK(s.amtInputRange * (rangeIn / s.amtInputAmp))
}

func (s *Smooth) namedInputSum(pin string) float64 {
	var sum float64
	for _, w := range s.namedInputs[pin] {
		sum += w.Get()
	}
	return sum
}

func (s *Smooth) compute() float64 {
	if s.modAmt {
		s.modAmtUpdate()
	}
	var inp float64
	for _, w := range s.inputs {
		inp += w.Get()
	}

	delta := inp - s.y0
	// The signum term is an absolute step (int units in Java), so divide
	// by intScale; the delta term scales with the signal and ports as-is.
	// signum(0) is 0, so a flat signal gets no constant nudge (no DC drift).
	var sgn float64
	if delta > 0 {
		sgn = 1
	} else if delta < 0 {
		sgn = -1
	}
	s.y0 += sgn*s.k/intScale + delta*s.k
	if (s.y0 < inp && delta < 0) || (s.y0 > inp && delta > 0) {
		s.y0 = inp // don't overshoot the target
	}
	return s.levelScale * s.y0 * s.compScale
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

// inAmpPair mirrors ConfigHelper.getInAmpPair: reads a nested map under
// key, requiring both an integer `amp` and a float `prop`. Returns
// ok=false (no modulation) if the block or either key is absent.
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
