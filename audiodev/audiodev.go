// Package audiodev wraps malgo (miniaudio) playback-device discovery and
// selection for the live synth and the audioInfo tool.
//
// It is deliberately separate from the core `audio` package: malgo pulls
// in CGo/miniaudio, and the offline renderer (cmd/p, which imports
// `audio`) must stay CGo-free. Only the live commands import this package.
package audiodev

import (
	"fmt"
	"strings"

	"github.com/gen2brain/malgo"
)

// ListOutputDevices returns the playback devices the context can see.
func ListOutputDevices(ctx *malgo.AllocatedContext) ([]malgo.DeviceInfo, error) {
	return ctx.Devices(malgo.Playback)
}

// SelectOutputDevice picks a playback device by case-insensitive
// substring. An empty substring selects the system default (returning a
// nil device, which malgo treats as "use the default"). The returned
// label is a human-readable name for logging.
func SelectOutputDevice(ctx *malgo.AllocatedContext, substr string) (dev *malgo.DeviceInfo, label string, err error) {
	devs, err := ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, "", err
	}
	if substr == "" {
		for i := range devs {
			if devs[i].IsDefault > 0 {
				return &devs[i], devs[i].Name() + " (default)", nil
			}
		}
		return nil, "(system default)", nil
	}
	sub := strings.ToLower(substr)
	for i := range devs {
		if strings.Contains(strings.ToLower(devs[i].Name()), sub) {
			return &devs[i], devs[i].Name(), nil
		}
	}
	return nil, "", fmt.Errorf("no audio output device matched %q", substr)
}
