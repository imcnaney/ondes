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
func (i *Instant) Seconds() float64 {
	return float64(i.sample) / float64(i.sr)
}

func (i *Instant) AddPhaseClock() *PhaseClock {
	pc := &PhaseClock{sr: i.sr}
	i.clocks = append(i.clocks, pc)
	return pc
}

func (i *Instant) Next() {
	i.sample++
	for _, c := range i.clocks {
		c.tick()
	}
}
