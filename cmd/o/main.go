// cmd/o is the live synth: it listens on a MIDI input port and plays the
// notes through a loaded patch to an audio output device in real time. It
// is the Go counterpart of the Java `o` script (App + SynthSession +
// MidiListenerThread + MonoMainMix).
//
//	o -in <substr>  -patch <name>     play patch live from a MIDI port
//	o -out <substr>                   pick an audio output by substring
//	o -buffer-size <n>                audio period in frames (latency)
//	o -hold                           suppress note-offs (drone)
//	o -list                           list MIDI inputs + audio outputs
//
// Threading model: the engine (synth.Synth) is single-threaded and is
// owned exclusively by the audio callback. The MIDI callback runs on a
// different thread, so it never touches the engine directly - it pushes
// note/CC commands onto a buffered channel with a non-blocking send. The
// audio callback drains that channel at the top of each buffer (applying
// the commands to the engine it owns) before rendering the buffer's
// samples. No lock sits on the per-sample path. This mirrors Java's
// MidiListenerThread queue, just drained on the audio thread.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/gen2brain/malgo"
	"gitlab.com/gomidi/midi/v2"

	// rtmidi is the concrete MIDI driver; registered here (not in the
	// mididev library) so offline builds don't link its CGo dependency.
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"

	"ondes/audio"
	"ondes/audiodev"
	"ondes/mididev"
	"ondes/patch"
	"ondes/synth"

	// Register every component type, exactly as cmd/p does. Without these
	// blank imports patch.Apply fails for every note and renders silence.
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

type cmdKind uint8

const (
	cmdNoteOn cmdKind = iota
	cmdNoteOff
	cmdCC
)

// command is a MIDI event handed from the MIDI thread to the audio thread.
// It is a value type so enqueuing never allocates.
type command struct {
	kind   cmdKind
	ch     uint8
	d1, d2 uint8
}

// patchAssign is one -patch value: a default patch (ch == -1) or a
// channel-specific one (ch is 0-based).
type patchAssign struct {
	ch   int
	name string
}

// patchList collects repeatable -patch flags. A bare `name` sets the
// default patch for all channels; `chan:name` (channel 1-16) overrides a
// single channel for multi-timbral play.
type patchList []patchAssign

func (pl *patchList) String() string { return fmt.Sprintf("%v", []patchAssign(*pl)) }

func (pl *patchList) Set(v string) error {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		ch, err := strconv.Atoi(v[:i])
		if err != nil || ch < 1 || ch > 16 {
			return fmt.Errorf("bad channel in %q (want chan:name, channel 1-16)", v)
		}
		*pl = append(*pl, patchAssign{ch: ch - 1, name: v[i+1:]})
		return nil
	}
	*pl = append(*pl, patchAssign{ch: -1, name: v})
	return nil
}

func main() {
	inFlag := flag.String("in", "", "MIDI input port substring")
	outFlag := flag.String("out", "", "audio output device substring (default: system default)")
	var patches patchList
	flag.Var(&patches, "patch", "patch to load: `name` (default for all channels) or `chan:name` (channel 1-16); repeatable")
	bufFlag := flag.Int("buffer-size", 1024, "audio period size in frames (lower = less latency, higher = more stable)")
	holdFlag := flag.Bool("hold", false, "suppress note-offs to sustain a drone")
	listFlag := flag.Bool("list", false, "list MIDI input ports and audio output devices, then exit")
	srFlag := flag.Int("sample-rate", audio.SampleRate, "sample rate (default matches the regression-tested engine path)")
	flag.Parse()

	// A single positional arg sets the default patch (matches `o foo`).
	if flag.NArg() == 1 {
		patches = append(patches, patchAssign{ch: -1, name: flag.Arg(0)})
	}
	if len(patches) == 0 {
		patches = append(patches, patchAssign{ch: -1, name: "sine"})
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(string) {})
	if err != nil {
		die("malgo InitContext: %v", err)
	}
	defer func() { _ = ctx.Uninit(); ctx.Free() }()

	if *listFlag {
		listAll(ctx)
		return
	}

	if *inFlag == "" {
		die("no MIDI input requested - pass -in <substr> (or -list to see ports)")
	}

	defaultPatch, chPatch, err := loadPatches(patches)
	if err != nil {
		die("patch: %v", err)
	}
	// Pass a true nil interface (not a typed nil *patch.Patch) when there
	// is no default, so patchFor returns a genuinely nil patch.
	var defPatch synth.Patch
	if defaultPatch != nil {
		defPatch = defaultPatch
	}
	eng := synth.New(*srFlag, defPatch)
	for ch, p := range chPatch {
		eng.SetChannelPatch(ch, p)
	}

	outDev, outLabel, err := audiodev.SelectOutputDevice(ctx, *outFlag)
	if err != nil {
		die("audio: %v", err)
	}

	// MIDI -> audio command queue. Buffered so a burst of events (a chord)
	// doesn't block the MIDI driver thread; on overflow we drop and count.
	q := make(chan command, 1024)
	var dropped uint64

	dataCb := func(out, _ []byte, frames uint32) {
		// Drain every queued command before rendering this buffer.
		for {
			select {
			case c := <-q:
				switch c.kind {
				case cmdNoteOn:
					eng.NoteOn(c.ch, c.d1, c.d2)
				case cmdNoteOff:
					eng.NoteOff(c.ch, c.d1)
				case cmdCC:
					eng.ControlChange(c.ch, c.d1, c.d2)
				}
			default:
				goto render
			}
		}
	render:
		// Engine yields mono float64 in [-1,1] (already limited); fan it
		// out to interleaved stereo float32 for malgo.
		buf := unsafe.Slice((*float32)(unsafe.Pointer(&out[0])), frames*2)
		for i := uint32(0); i < frames; i++ {
			s := float32(eng.Step())
			buf[2*i] = s
			buf[2*i+1] = s
		}
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatF32
	cfg.Playback.Channels = 2
	cfg.SampleRate = uint32(*srFlag)
	cfg.PeriodSizeInFrames = uint32(*bufFlag)
	if outDev != nil {
		cfg.Playback.DeviceID = outDev.ID.Pointer()
	}

	dev, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: dataCb})
	if err != nil {
		die("malgo InitDevice: %v", err)
	}
	defer dev.Uninit()
	if err := dev.Start(); err != nil {
		die("device Start: %v", err)
	}

	inPort, err := mididev.FindInPort(*inFlag)
	if err != nil {
		die("MIDI: %v", err)
	}
	stop, err := midi.ListenTo(inPort, func(msg midi.Message, _ int32) {
		var ch, key, vel, cc, ccVal uint8
		var c command
		switch {
		case msg.GetNoteStart(&ch, &key, &vel):
			c = command{kind: cmdNoteOn, ch: ch, d1: key, d2: vel}
		case msg.GetNoteEnd(&ch, &key):
			if *holdFlag {
				return // drone: never release
			}
			c = command{kind: cmdNoteOff, ch: ch, d1: key}
		case msg.GetControlChange(&ch, &cc, &ccVal):
			c = command{kind: cmdCC, ch: ch, d1: cc, d2: ccVal}
		default:
			return
		}
		select {
		case q <- c:
		default:
			atomic.AddUint64(&dropped, 1) // queue full; drop rather than block MIDI
		}
	})
	if err != nil {
		die("MIDI ListenTo: %v", err)
	}
	defer stop()

	periodMs := float64(*bufFlag) / float64(*srFlag) * 1000
	printPatches(defaultPatch, chPatch)
	fmt.Printf("audio: %s\n", outLabel)
	fmt.Printf("       %d Hz, %d-frame period (~%.1f ms)\n", *srFlag, *bufFlag, periodMs)
	fmt.Printf("midi:  %s\n", inPort.String())
	if *holdFlag {
		fmt.Println("hold:  note-offs suppressed")
	}
	fmt.Println("listening - Ctrl-C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	if d := atomic.LoadUint64(&dropped); d > 0 {
		fmt.Printf("\n(dropped %d MIDI events under load - try a larger -buffer-size)\n", d)
	}
}

// loadPatches resolves the -patch assignments into a default patch
// (possibly nil) and a per-channel map, loading each named patch once.
func loadPatches(assigns patchList) (def *patch.Patch, byChan map[uint8]*patch.Patch, err error) {
	cache := map[string]*patch.Patch{}
	load := func(name string) (*patch.Patch, error) {
		if p, ok := cache[name]; ok {
			return p, nil
		}
		p, err := patch.Load(name)
		if err != nil {
			return nil, err
		}
		cache[name] = p
		return p, nil
	}
	byChan = map[uint8]*patch.Patch{}
	for _, a := range assigns {
		p, err := load(a.name)
		if err != nil {
			return nil, nil, fmt.Errorf("%q: %w", a.name, err)
		}
		if a.ch < 0 {
			def = p
		} else {
			byChan[uint8(a.ch)] = p
		}
	}
	return def, byChan, nil
}

func printPatches(def *patch.Patch, byChan map[uint8]*patch.Patch) {
	if def != nil {
		fmt.Printf("patch: %s\n", def.Name())
	}
	for ch := 0; ch < 16; ch++ {
		if p, ok := byChan[uint8(ch)]; ok {
			fmt.Printf("       ch%d: %s\n", ch+1, p.Name())
		}
	}
}

func listAll(ctx *malgo.AllocatedContext) {
	devs, err := audiodev.ListOutputDevices(ctx)
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
	ports := mididev.ListInPorts()
	if len(ports) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, name := range ports {
		fmt.Printf("  - %s\n", name)
	}
}

func die(format string, args ...any) {
	log.Fatalf("o: "+format, args...)
}
