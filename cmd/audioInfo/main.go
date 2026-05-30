// cmd/audioInfo lists the audio output (playback) devices malgo can see,
// so you can find the substring to pass to `o -out`. It is the Go
// counterpart of the Java AudioInfo tool / ./audioInfo script.
package main

import (
	"fmt"
	"log"

	"github.com/gen2brain/malgo"

	"ondes/audiodev"
)

func main() {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		log.Fatalf("audioInfo: malgo InitContext: %v", err)
	}
	defer func() { _ = ctx.Uninit(); ctx.Free() }()

	devs, err := audiodev.ListOutputDevices(ctx)
	if err != nil {
		log.Fatalf("audioInfo: %v", err)
	}
	fmt.Println("audio output devices:")
	if len(devs) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, d := range devs {
		def := ""
		if d.IsDefault > 0 {
			def = " (default)"
		}
		fmt.Printf("  - %s%s\n", d.Name(), def)
	}
}
