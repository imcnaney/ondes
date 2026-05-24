// cmd/p renders a MIDI file through a synth patch into a WAV file. It
// mirrors the Java `./p <in.mid> <out.wav>` script but takes an
// explicit -patch flag (the Java tool defaults to looking at filename
// hints; we keep it simple).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"ondes/audio"
	"ondes/midi"
	"ondes/patch"
	"ondes/synth"

	// Register components.
	_ "ondes/component/wave"
)

func main() {
	patchName := flag.String("patch", "sine", "patch name to load")
	tailSec := flag.Float64("tail", 0.0023, "extra seconds of audio rendered after the last MIDI event (Java default is ~100 samples)")
	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s [-patch name] [-tail sec] <in.mid> <out.wav>\n", os.Args[0])
		os.Exit(2)
	}
	inMid, outWav := flag.Arg(0), flag.Arg(1)

	p, err := patch.Load(*patchName)
	if err != nil {
		log.Fatalf("patch: %v", err)
	}

	events, err := midi.ReadFile(inMid, audio.SampleRate)
	if err != nil {
		log.Fatalf("midi: %v", err)
	}
	if len(events) == 0 {
		log.Fatalf("midi: %s contains no note events", inMid)
	}

	last := events[len(events)-1].Sample
	total := last + int64(*tailSec*float64(audio.SampleRate))

	s := synth.New(audio.SampleRate, p)
	samples := make([]float64, total)

	start := time.Now()
	ei := 0
	for i := int64(0); i < total; i++ {
		for ei < len(events) && events[ei].Sample <= i {
			m := events[ei].Msg
			ch := m.Status & 0x0F
			switch {
			case m.IsNoteOn():
				s.NoteOn(ch, m.Data1, m.Data2)
			case m.IsNoteOff():
				s.NoteOff(ch, m.Data1)
			}
			ei++
		}
		samples[i] = s.Step()
	}
	elapsed := time.Since(start)

	if err := audio.WriteMono16(outWav, samples, audio.SampleRate); err != nil {
		log.Fatalf("wav: %v", err)
	}

	wallSec := elapsed.Seconds()
	audSec := float64(total) / float64(audio.SampleRate)
	fmt.Printf("rendered %d samples (%.2fs audio) in %.2fs wall - %.1fx realtime - %s\n",
		total, audSec, wallSec, audSec/wallSec, outWav)
}
