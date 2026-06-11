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
	_ "ondes/component/balancer"
	_ "ondes/component/controller"
	_ "ondes/component/echo"
	_ "ondes/component/env"
	_ "ondes/component/filter"
	_ "ondes/component/midinote"
	_ "ondes/component/mix"
	_ "ondes/component/opamp"
	_ "ondes/component/smooth"
	_ "ondes/component/wave"
)

func main() {
	patchName := flag.String("patch", "sine", "patch name to load")
	tailSec := flag.Float64("tail", 0.0023, "minimum extra seconds of audio after the last MIDI event (Java default ~100 samples)")
	maxTailSec := flag.Float64("max-tail", 30.0, "hard cap on how long to keep rendering while voices are still active")
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
	minEnd := last + int64(*tailSec*float64(audio.SampleRate))
	maxEnd := last + int64(*maxTailSec*float64(audio.SampleRate))

	s := synth.New(audio.SampleRate, p)
	samples := make([]float64, 0, minEnd)

	start := time.Now()
	ei := 0
	for i := int64(0); ; i++ {
		for ei < len(events) && events[ei].Sample <= i {
			m := events[ei].Msg
			ch := m.Status & 0x0F
			switch {
			case m.IsNoteOn():
				s.NoteOn(ch, m.Data1, m.Data2)
			case m.IsNoteOff():
				s.NoteOff(ch, m.Data1)
			case m.Status&0xF0 == 0xB0:
				s.ControlChange(ch, m.Data1, m.Data2)
			}
			ei++
		}
		samples = append(samples, s.Step())
		if i >= minEnd && ei >= len(events) && s.ActiveVoices() == 0 {
			break
		}
		if i >= maxEnd {
			break
		}
	}
	total := int64(len(samples))
	elapsed := time.Since(start)

	if err := audio.WriteMono16(outWav, samples, audio.SampleRate); err != nil {
		log.Fatalf("wav: %v", err)
	}

	wallSec := elapsed.Seconds()
	audSec := float64(total) / float64(audio.SampleRate)
	fmt.Printf("rendered %d samples (%.2fs audio) in %.2fs wall - %.1fx realtime - %s\n",
		total, audSec, wallSec, audSec/wallSec, outWav)
}
