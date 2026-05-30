package regression

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ondes/audio"
	"ondes/midi"
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

	"ondes/patch"
)

// TestRegressionParity renders every fixture in renders.lst through the
// in-process engine (the same path cmd/p drives) into a tempdir, then
// shells out to diff_summaries.py once to compare every fresh WAV against
// the committed Java reference summary within tolerance. This is the
// Go-side equivalent of regression/check.sh, which re-renders with Java.
func TestRegressionParity(t *testing.T) {
	if err := chdirModuleRoot(); err != nil {
		t.Fatalf("locating module root: %v", err)
	}

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; skipping summary diff")
	}

	rows, err := readRenderList(filepath.Join("regression", "renders.lst"))
	if err != nil {
		t.Fatalf("reading renders.lst: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("renders.lst contained no fixtures")
	}

	wavDir := t.TempDir()
	for _, r := range rows {
		mid := filepath.Join("regression", "fixtures", "mid", r.midi+".mid")
		wav := filepath.Join(wavDir, r.name+".wav")
		if err := renderFixture(r.patch, mid, wav); err != nil {
			t.Errorf("%s (%s): %v", r.name, r.patch, err)
		}
	}

	cmd := exec.Command(python, filepath.Join("regression", "diff_summaries.py"),
		"--ref", filepath.Join("regression", "fixtures", "summary"),
		"--wav", wavDir)
	out, err := cmd.CombinedOutput()
	t.Logf("diff_summaries.py output:\n%s", out)
	if err != nil {
		t.Fatalf("regression diff failed: %v", err)
	}
}

type renderRow struct{ name, midi, patch string }

// readRenderList parses renders.lst: whitespace-separated
// <wav-name> <midi-name> <patch-spec> rows, skipping blank and # lines.
func readRenderList(path string) ([]renderRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []renderRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		rows = append(rows, renderRow{name: fields[0], midi: fields[1], patch: fields[2]})
	}
	return rows, sc.Err()
}

// renderFixture mirrors cmd/p/main.go: load patch, read MIDI, drive the
// engine sample-by-sample, and write a mono 16-bit WAV. The tail and
// stop conditions match cmd/p so the output lines up with the Java
// reference renders the committed summaries were taken from.
func renderFixture(patchName, midiPath, wavPath string) error {
	p, err := patch.Load(patchName)
	if err != nil {
		return err
	}
	events, err := midi.ReadFile(midiPath, audio.SampleRate)
	if err != nil {
		return err
	}

	// tailSec/maxTailSec mirror cmd/p's defaults. Kept as variables so the
	// float->int64 conversion isn't a disallowed constant truncation.
	tailSec, maxTailSec := 0.0023, 30.0
	last := events[len(events)-1].Sample
	minEnd := last + int64(tailSec*float64(audio.SampleRate))
	maxEnd := last + int64(maxTailSec*float64(audio.SampleRate))

	s := synth.New(audio.SampleRate, p)
	samples := make([]float64, 0, minEnd)

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
	return audio.WriteMono16(wavPath, samples, audio.SampleRate)
}

// chdirModuleRoot walks up from the test's working directory to the
// directory containing go.mod, so patch.Load (which resolves ./program
// relative to CWD) and the regression/ paths above work regardless of
// where `go test` is invoked.
func chdirModuleRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return os.ErrNotExist
		}
		dir = parent
	}
}
