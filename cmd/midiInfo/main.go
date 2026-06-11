// cmd/midiInfo lists the MIDI input and output ports, so you can find the
// substring to pass to `o -in`. It is the Go counterpart of the Java
// MidiInfo tool / ./midiInfo script.
package main

import (
	"fmt"

	// rtmidi is the concrete MIDI driver.
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"

	"ondes/mididev"
)

func main() {
	printPorts("MIDI input ports", mididev.ListInPorts())
	fmt.Println()
	printPorts("MIDI output ports", mididev.ListOutPorts())
}

func printPorts(label string, ports []string) {
	fmt.Printf("%s:\n", label)
	if len(ports) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, name := range ports {
		fmt.Printf("  - %s\n", name)
	}
}
