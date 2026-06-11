package synth

type PhaseClock struct {
	frequency float64
	delta     float64
	phase     float64
	sr        int
}

func (p *PhaseClock) SetFrequency(hz float64) {
	p.frequency = hz
	p.delta = hz / float64(p.sr)
}

func (p *PhaseClock) Frequency() float64 { return p.frequency }
func (p *PhaseClock) Phase() float64     { return p.phase }
func (p *PhaseClock) ResetPhase()        { p.phase = 0 }

func (p *PhaseClock) tick() {
	p.phase += p.delta
	for p.phase >= 1 {
		p.phase -= 1
	}
}

// Instant tracks the current sample number and drives every PhaseClock.
type Instant struct {
	sr     int
	sample int64
	clocks []*PhaseClock
}

func NewInstant(sr int) *Instant { return &Instant{sr: sr} }

func (i *Instant) SampleRate() int { return i.sr }
func (i *Instant) Sample() int64   { return i.sample }

// ActiveClocks reports how many phase clocks are currently registered.
// Exposed mainly so tests can assert that clocks don't leak across notes.
func (i *Instant) ActiveClocks() int { return len(i.clocks) }
func (i *Instant) Seconds() float64 {
	return float64(i.sample) / float64(i.sr)
}

func (i *Instant) AddPhaseClock() *PhaseClock {
	pc := &PhaseClock{sr: i.sr}
	i.clocks = append(i.clocks, pc)
	return pc
}

// RemoveClock unregisters a phase clock so Next no longer ticks it. This
// is how a finished voice stops leaking clocks onto the Instant: without
// it, every note ever played would keep ticking for the life of the synth
// (harmless for short offline renders, an unbounded leak for live play).
// Order is irrelevant - the clocks are independent - so a swap-remove is
// fine.
func (i *Instant) RemoveClock(pc *PhaseClock) {
	for j, c := range i.clocks {
		if c == pc {
			last := len(i.clocks) - 1
			i.clocks[j] = i.clocks[last]
			i.clocks[last] = nil
			i.clocks = i.clocks[:last]
			return
		}
	}
}

func (i *Instant) Next() {
	i.sample++
	for _, c := range i.clocks {
		c.tick()
	}
}
