// Package env implements the multi-segment envelope component. Mirrors
// ondes.synth.envelope.Envelope: a list of (rate, level [, option])
// steps that the level walks through, with markers for re-trigger
// (where to jump on a re-press), hold (sustain until note-off),
// release (where note-off jumps to), and alt-release (sustain pedal
// up exit path).
package env

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("env", func() component.Component { return &Env{} })
}

// Env attenuates the sum of its inputs by a per-sample level, and
// optionally signals voice termination when its envelope completes.
type Env struct {
	voice  *synth.Voice
	out    *synth.Wire
	inputs []*synth.Wire

	steps                                []*step
	curStep                              int
	reTrigger, hold, release, altRelease int

	curLevel   float64
	noteOn     bool
	firstNote  bool
	preRelease bool
	exit       bool
	levelScale float64
	chanNo     uint8
	noteNo     uint8
	ended      bool
	sampleRate int
}

func (e *Env) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	e.voice = v
	e.sampleRate = v.Synth().SampleRate()
	e.firstNote = true
	e.preRelease = true
	e.levelScale = 1
	e.reTrigger, e.hold, e.release, e.altRelease = -1, -1, -1, -1

	if b, ok := spec["exit"].(bool); ok && b {
		e.exit = true
		v.SetWaitForEnv(true)
	}
	if ls, ok := numeric(spec["level-scale"]); ok {
		e.levelScale = ls
	}

	pts, ok := spec["points"].([]any)
	if !ok {
		return fmt.Errorf("env: points: list required")
	}
	if err := e.parsePoints(pts); err != nil {
		return err
	}

	e.out = v.NewWire(e.compute)
	return nil
}

func (e *Env) Output() *synth.Wire { return e.out }

func (e *Env) AddInput(_ string, w *synth.Wire) { e.inputs = append(e.inputs, w) }

func (e *Env) OnMidi(m synth.MidiMsg) {
	switch {
	case m.IsNoteOn():
		e.noteOn = true
		e.chanNo = m.Status & 0x0F
		e.noteNo = m.Data1
		if e.firstNote {
			e.curStep = 0
		} else if e.reTrigger >= 0 {
			e.curStep = e.reTrigger
		} else {
			e.curStep = 0
		}
		e.firstNote = false
		e.ended = false
		e.preRelease = true
	case m.IsNoteOff():
		e.noteOn = false
		e.preRelease = false
		if e.curStep == e.hold {
			e.setCurStep(e.hold + 1)
		} else if e.release >= 0 {
			e.setCurStep(e.release)
		}
	}
}

func (e *Env) setCurStep(idx int) {
	if idx < 0 || idx >= len(e.steps) {
		return
	}
	e.curStep = idx
	if idx == e.release {
		e.preRelease = false
	}
	if e.steps[idx].kind == stepHold {
		e.steps[idx].holdStartSample = e.voice.Synth().Instant().Sample()
	}
}

func (e *Env) compute() float64 {
	if e.ended {
		return 0
	}
	s := e.steps[e.curStep]
	level, done := s.nextVal(e.curLevel, e.voice.Synth().Instant().Sample())
	e.curLevel = level
	if done {
		e.advance()
	}
	var sum float64
	for _, w := range e.inputs {
		sum += w.Get()
	}
	return sum * (e.curLevel / 100.0) * e.levelScale
}

func (e *Env) advance() {
	// Holds wait for note-off (or sustain release).
	if e.curStep == e.hold && e.noteOn {
		return
	}
	last := len(e.steps) - 1
	endIdx := last
	if e.altRelease > 0 {
		endIdx = e.altRelease - 1
	}
	if e.curStep == endIdx {
		if e.exit && e.curLevel == 0 {
			e.voice.Synth().QueueNoteEnd(e.chanNo, e.noteNo)
			e.ended = true
		}
		return
	}
	e.setCurStep(e.curStep + 1)
}

// parsePoints walks the YAML list, building Step / Hold instances and
// recording the option-marker indices. The Java parser inserts a 0,0
// step at the front if the first step's rate is nonzero, ensures the
// final step lands at level 0, and ensures the step preceding altRelease
// lands at 0 too. We mirror all three.
func (e *Env) parsePoints(pts []any) error {
	for _, raw := range pts {
		var line string
		switch v := raw.(type) {
		case string:
			line = v
		case []any:
			parts := make([]string, len(v))
			for i, x := range v {
				parts[i] = fmt.Sprintf("%v", x)
			}
			line = strings.Join(parts, " ")
		case nil:
			continue
		default:
			line = fmt.Sprintf("%v", v)
		}
		tokens := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' || r == ',' })
		if len(tokens) < 2 {
			return fmt.Errorf("env: bad point %q", line)
		}
		rate, err := strconv.ParseFloat(tokens[0], 64)
		if err != nil {
			return fmt.Errorf("env: bad rate %q", tokens[0])
		}
		level, err := strconv.ParseFloat(tokens[1], 64)
		if err != nil {
			return fmt.Errorf("env: bad level %q", tokens[1])
		}
		var opts []string
		for _, t := range tokens[2:] {
			opts = append(opts, strings.ReplaceAll(t, "-", ""))
		}

		// 0,0 lead-in if the first step doesn't start there.
		if len(e.steps) == 0 && rate != 0 {
			e.steps = append(e.steps, newStep(0, 0, e.sampleRate))
		}

		isAltRelease := false
		for _, o := range opts {
			if o == "altrelease" {
				isAltRelease = true
			}
		}
		// Repeated level becomes a Hold, unless this is the alt-release.
		if n := len(e.steps); n > 0 && !isAltRelease && e.steps[n-1].level == level {
			e.steps = append(e.steps, newHold(rate, level, e.sampleRate))
		} else {
			e.steps = append(e.steps, newStep(rate, level, e.sampleRate))
		}
		for _, o := range opts {
			switch o {
			case "retrigger":
				e.reTrigger = len(e.steps) - 1
			case "hold":
				e.hold = len(e.steps) - 1
			case "release":
				e.release = len(e.steps) - 1
			case "altrelease":
				e.altRelease = len(e.steps) - 1
			}
		}
	}

	if n := len(e.steps); n == 0 || e.steps[n-1].level != 0 {
		e.steps = append(e.steps, newStep(0, 0, e.sampleRate))
	}
	if e.hold >= 0 && e.steps[e.hold].level == 0 {
		e.hold = -1
	}
	if e.altRelease > 0 && e.steps[e.altRelease-1].level != 0 {
		zero := newStep(0, 0, e.sampleRate)
		e.steps = append(e.steps[:e.altRelease], append([]*step{zero}, e.steps[e.altRelease:]...)...)
		e.altRelease++
	}
	if e.altRelease > 0 {
		e.release = e.altRelease - 1
	} else if e.release < 0 {
		if e.hold >= 0 {
			e.release = e.hold + 1
			if e.release > len(e.steps)-1 {
				e.release = len(e.steps) - 1
			}
		} else {
			e.release = len(e.steps) - 1
		}
	}
	return nil
}

type stepKind int

const (
	stepLinear stepKind = iota
	stepHold
)

// step represents one envelope segment. Linear segments use the
// d/k/m formula from the Java source (a logarithmic-ish approach to
// `level` per sample). Hold segments wait `rate` ms of real time
// before reporting done.
type step struct {
	kind            stepKind
	rate, level     float64 // rate in ms, level 0..100
	sampleRate      int
	k, m            float64
	holdStartSample int64
}

func newStep(rate, level float64, sr int) *step {
	s := &step{kind: stepLinear, rate: math.Max(0, rate), level: clip100(level), sampleRate: sr}
	if rate > 0 {
		d := rate * float64(sr) / 1000.0 / 4.616
		s.k = 1.0 / d
		s.m = 1.0 / d
	}
	return s
}

func newHold(rate, level float64, sr int) *step {
	s := newStep(rate, level, sr)
	s.kind = stepHold
	return s
}

func (s *step) nextVal(cur float64, curSample int64) (float64, bool) {
	if s.kind == stepHold {
		if cur != s.level {
			return s.advanceLinear(cur)
		}
		holdSamples := int64(s.rate * float64(s.sampleRate) / 1000.0)
		return s.level, curSample-s.holdStartSample >= holdSamples
	}
	return s.advanceLinear(cur)
}

func (s *step) advanceLinear(cur float64) (float64, bool) {
	if s.rate == 0 || cur == s.level {
		return s.level, true
	}
	delta := s.level - cur
	sign := 1.0
	if delta < 0 {
		sign = -1.0
	}
	next := cur + sign*s.k + delta*s.m
	if (cur > s.level && next <= s.level) || (cur < s.level && next >= s.level) {
		return s.level, true
	}
	return clip100(next), false
}

func clip100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
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
