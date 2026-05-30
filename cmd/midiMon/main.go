// cmd/midiMon prints incoming MIDI messages from an input port in real
// time, for checking that a controller is connected and what it sends. It
// is the Go counterpart of the Java MidiMonitor tool / ./midiMon script.
//
//	midiMon              monitor the first available MIDI input port
//	midiMon -in <substr> monitor the first port matching <substr>
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/gomidi/midi/v2"

	// rtmidi is the concrete MIDI driver.
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"

	"ondes/mididev"
)

func main() {
	inFlag := flag.String("in", "", "MIDI input port substring (default: first available)")
	flag.Parse()

	ports := mididev.ListInPorts()
	if len(ports) == 0 {
		log.Fatal("midiMon: no MIDI input ports found")
	}

	// An empty -in matches the first port; otherwise match by substring.
	inPort, err := mididev.FindInPort(*inFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "midiMon:", err)
		fmt.Fprintln(os.Stderr, "available input ports:")
		for _, name := range ports {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
		os.Exit(1)
	}

	stop, err := midi.ListenTo(inPort, func(msg midi.Message, _ int32) {
		var ch, key, vel, cc, ccVal, prog uint8
		switch {
		case msg.GetNoteStart(&ch, &key, &vel):
			fmt.Printf("ch%-2d  note-on   key=%-3d vel=%d\n", ch+1, key, vel)
		case msg.GetNoteEnd(&ch, &key):
			fmt.Printf("ch%-2d  note-off  key=%d\n", ch+1, key)
		case msg.GetControlChange(&ch, &cc, &ccVal):
			fmt.Printf("ch%-2d  cc        num=%-3d val=%d\n", ch+1, cc, ccVal)
		case msg.GetProgramChange(&ch, &prog):
			fmt.Printf("ch%-2d  program   %d\n", ch+1, prog)
		default:
			fmt.Printf("      %s\n", msg.String())
		}
	})
	if err != nil {
		log.Fatalf("midiMon: ListenTo: %v", err)
	}
	defer stop()

	fmt.Printf("monitoring: %s\n", inPort.String())
	fmt.Println("Ctrl-C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
