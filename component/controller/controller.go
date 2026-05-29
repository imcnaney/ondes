// Package controller implements the `controller` component: a MIDI CC
// listener that outputs the (scaled) current value of one numbered
// controller. Mirrors ondes.synth.wire.Controller.
//
// Output level = min + (lvl/127) * (max - min), where (min, max) are
// parsed from the `amp:` config (single value -> min=0; "min max" -> both).
//
// CC dispatch from the SMF reader is not implemented yet, so for the
// regression fixtures (which contain only note-on/off) the controller
// sits at 0 forever. That is the correct behavior for brass.yaml's
// ctrl12/ctrl13 - the patch is designed to receive live CC and quietly
// no-ops when none arrive.
package controller

import (
	"fmt"
	"strings"

	"ondes/component"
	"ondes/synth"
)

func init() {
	component.Register("controller", func() component.Component { return &Controller{} })
}

type Controller struct {
	number   uint8
	minLevel float64
	maxLevel float64
	level    float64
	out      *synth.Wire
}

func (c *Controller) Configure(spec component.Spec, v *synth.Voice, _ string) error {
	num, ok := numeric(spec["number"])
	if !ok {
		return fmt.Errorf("controller: number required")
	}
	c.number = uint8(num)
	min, max, err := parseMinMax(spec["amp"])
	if err != nil {
		return err
	}
	c.minLevel = min
	c.maxLevel = max
	c.out = v.NewWire(c.compute)
	return nil
}

func (c *Controller) Output() *synth.Wire { return c.out }

func (c *Controller) compute() float64 {
	return c.level / 32767.0
}

func (c *Controller) OnMidi(m synth.MidiMsg) {
	if m.Status&0xF0 != 0xB0 || m.Data1 != c.number {
		return
	}
	c.level = c.minLevel + float64(m.Data2)/127.0*(c.maxLevel-c.minLevel)
}

// parseMinMax handles the Java getMinMaxLevel convention: a single
// number is treated as (0, n); a "min max" string (or YAML list-ish) is
// taken as-is.
func parseMinMax(v any) (float64, float64, error) {
	switch x := v.(type) {
	case int:
		return 0, float64(x), nil
	case int64:
		return 0, float64(x), nil
	case float64:
		return 0, x, nil
	case string:
		var nums []float64
		for _, tok := range strings.FieldsFunc(x, func(r rune) bool { return r == ' ' || r == ',' }) {
			var f float64
			if _, err := fmt.Sscanf(tok, "%f", &f); err != nil {
				return 0, 0, fmt.Errorf("controller: bad amp %q", x)
			}
			nums = append(nums, f)
		}
		switch len(nums) {
		case 1:
			return 0, nums[0], nil
		case 2:
			return nums[0], nums[1], nil
		}
	}
	return 0, 0, fmt.Errorf("controller: amp must be a number or \"min max\" string")
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
