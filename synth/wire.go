// Package synth is the audio engine: sample clock, voices, wire graph.
package synth

// A Wire is one node's output in the pull-driven audio graph. Each sample
// it can be Get() any number of times; only the first call computes; the
// rest return the latched value. This is what lets a component feed its
// own output back in (FM) without infinite recursion. Reset() runs once
// per sample at the top of the synth main loop.
type Wire struct {
	compute func() float64
	cached  float64
	visited bool
}

func NewWire(compute func() float64) *Wire {
	return &Wire{compute: compute}
}

func (w *Wire) Get() float64 {
	if !w.visited {
		w.cached = w.compute()
		w.visited = true
	}
	return w.cached
}

func (w *Wire) Reset() {
	w.visited = false
}
