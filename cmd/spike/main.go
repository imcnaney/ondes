// spike: minimal audio + MIDI test for the Go port.
//
//	spike -list           list MIDI input ports and audio output devices
//	spike -test           play a 1-second A4 sine on the default output
//	spike -in <substr>    listen on the matching MIDI port; sine voices
//	                      track note-on / note-off, simple polyphony
//	spike -out <substr>   pick an audio output by substring (default: system)
//	spike -buffer <n>     audio period size in frames (default 256)
//
// The goal is to prove the audio + MIDI portability story before
// porting the engine: can we get sub-Java-latency output on macOS via
// malgo (miniaudio → CoreAudio) and MIDI in via rtmidi?
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/drivers"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"
)

const sampleRate = 44100

type voice struct {
	phase float64
	delta float64
	amp   float64
}

type synth struct {
	mu     sync.Mutex
	voices map[uint8]*voice
}

func newSynth() *synth { return &synth{voices: map[uint8]*voice{}} }

func noteFreq(note uint8) float64 {
	return 440 * math.Pow(2, (float64(note)-69)/12)
}

func (s *synth) noteOn(note, vel uint8) {
	s.mu.Lock()
	s.voices[note] = &voice{
		delta: noteFreq(note) / sampleRate,
		amp:   float64(vel) / 127 * 0.15, // headroom for polyphony
	}
	s.mu.Unlock()
}

func (s *synth) noteOff(note uint8) {
	s.mu.Lock()
	delete(s.voices, note)
	s.mu.Unlock()
}

// fillStereoF32 writes interleaved float32 stereo into the malgo buffer.
func (s *synth) fillStereoF32(buf []byte, frames uint32) {
	out := unsafe.Slice((*float32)(unsafe.Pointer(&buf[0])), frames*2)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := uint32(0); i < frames; i++ {
		var sum float64
		for _, v := range s.voices {
			sum += math.Sin(2*math.Pi*v.phase) * v.amp
			v.phase += v.delta
			if v.phase >= 1 {
				v.phase -= 1
			}
		}
		if sum > 1 {
			sum = 1
		} else if sum < -1 {
			sum = -1
		}
		f := float32(sum)
		out[2*i] = f
		out[2*i+1] = f
	}
}

func main() {
	listFlag := flag.Bool("list", false, "list devices and exit")
	testFlag := flag.Bool("test", false, "play 1s A4 sine and exit (no MIDI)")
	inFlag := flag.String("in", "", "MIDI input port substring")
	outFlag := flag.String("out", "", "audio output device substring")
	bufFlag := flag.Int("buffer", 256, "audio period size in frames")
	flag.Parse()

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		die("malgo InitContext: %v", err)
	}
	defer func() { _ = ctx.Uninit(); ctx.Free() }()

	if *listFlag {
		listDevices(ctx)
		return
	}

	outDev, outLabel := selectOutputDevice(ctx, *outFlag)

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = 2
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = uint32(*bufFlag)
	if outDev != nil {
		cfg.Playback.DeviceID = outDev.ID.Pointer()
	}

	syn := newSynth()
	cb := malgo.DeviceCallbacks{
		Data: func(out, _ []byte, frames uint32) { syn.fillStereoF32(out, frames) },
	}
	dev, err := malgo.InitDevice(ctx.Context, cfg, cb)
	if err != nil {
		die("malgo InitDevice: %v", err)
	}
	defer dev.Uninit()
	if err := dev.Start(); err != nil {
		die("device Start: %v", err)
	}

	periodMs := float64(*bufFlag) / sampleRate * 1000
	fmt.Printf("audio: %s\n", outLabel)
	fmt.Printf("       %d Hz, %d-frame period (~%.1f ms)\n", sampleRate, *bufFlag, periodMs)

	if *testFlag {
		syn.noteOn(69, 100)
		time.Sleep(time.Second)
		syn.noteOff(69)
		time.Sleep(50 * time.Millisecond)
		return
	}

	if *inFlag == "" {
		fmt.Println("\nNo MIDI input requested. Use -in <substr>, or -test for a 1s sine.")
		return
	}

	inPort, err := findInPort(*inFlag)
	if err != nil {
		die("MIDI: %v", err)
	}
	stop, err := midi.ListenTo(inPort, func(msg midi.Message, _ int32) {
		var ch, key, vel uint8
		switch {
		case msg.GetNoteStart(&ch, &key, &vel):
			syn.noteOn(key, vel)
		case msg.GetNoteEnd(&ch, &key):
			syn.noteOff(key)
		}
	})
	if err != nil {
		die("MIDI ListenTo: %v", err)
	}
	defer stop()

	fmt.Printf("midi:  %s\n", inPort.String())
	fmt.Println("listening — Ctrl-C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spike: "+format+"\n", args...)
	os.Exit(1)
}

func listDevices(ctx *malgo.AllocatedContext) {
	devs, err := ctx.Devices(malgo.Playback)
	if err != nil {
		die("listing audio devices: %v", err)
	}
	fmt.Println("audio output devices:")
	for _, d := range devs {
		def := ""
		if d.IsDefault > 0 {
			def = " (default)"
		}
		fmt.Printf("  - %s%s\n", d.Name(), def)
	}

	fmt.Println("\nMIDI input ports:")
	ports := midi.GetInPorts()
	if len(ports) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, p := range ports {
		fmt.Printf("  - %s\n", p.String())
	}
}

func selectOutputDevice(ctx *malgo.AllocatedContext, substr string) (*malgo.DeviceInfo, string) {
	devs, err := ctx.Devices(malgo.Playback)
	if err != nil {
		die("listing audio devices: %v", err)
	}
	if substr == "" {
		for i := range devs {
			if devs[i].IsDefault > 0 {
				return &devs[i], devs[i].Name() + " (default)"
			}
		}
		return nil, "(system default)"
	}
	sub := strings.ToLower(substr)
	for i := range devs {
		if strings.Contains(strings.ToLower(devs[i].Name()), sub) {
			return &devs[i], devs[i].Name()
		}
	}
	die("no audio output device matched %q", substr)
	return nil, ""
}

func findInPort(substr string) (drivers.In, error) {
	sub := strings.ToLower(substr)
	for _, p := range midi.GetInPorts() {
		if strings.Contains(strings.ToLower(p.String()), sub) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no MIDI input port matched %q", substr)
}
