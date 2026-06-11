// Package mididev wraps gomidi input/output port discovery for the live
// synth, midiInfo, and midiMon tools.
//
// It calls only the driver-agnostic gomidi API; the concrete driver
// (rtmidi) is registered by a blank import in the commands that need it
// (`_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"`), so the offline
// renderer never links the rtmidi CGo dependency.
package mididev

import (
	"fmt"
	"strings"

	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/drivers"
)

// ListInPorts returns the names of the available MIDI input ports.
func ListInPorts() []string {
	ports := midi.GetInPorts()
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = p.String()
	}
	return out
}

// ListOutPorts returns the names of the available MIDI output ports.
func ListOutPorts() []string {
	ports := midi.GetOutPorts()
	out := make([]string, len(ports))
	for i, p := range ports {
		out[i] = p.String()
	}
	return out
}

// FindInPort returns the first MIDI input port whose name contains substr
// (case-insensitive).
func FindInPort(substr string) (drivers.In, error) {
	sub := strings.ToLower(substr)
	for _, p := range midi.GetInPorts() {
		if strings.Contains(strings.ToLower(p.String()), sub) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no MIDI input port matched %q", substr)
}
