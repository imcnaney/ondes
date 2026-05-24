package synth

// Junction sums any number of input wires into a single output wire. The
// output wire is registered with the Voice so it gets reset each sample.
type Junction struct {
	inputs []*Wire
	out    *Wire
}

func NewJunction(v *Voice) *Junction {
	j := &Junction{}
	j.out = v.NewWire(j.sum)
	return j
}

func (j *Junction) AddInput(w *Wire) {
	j.inputs = append(j.inputs, w)
}

func (j *Junction) Output() *Wire { return j.out }

func (j *Junction) sum() float64 {
	var s float64
	for _, w := range j.inputs {
		s += w.Get()
	}
	return s
}
