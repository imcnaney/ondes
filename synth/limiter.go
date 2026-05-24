package synth

// Limiter is the soft peak limiter applied to the main mix, ported
// from Java's ondes.synth.envelope.Limiter + MaxTrackerPQ.
//
// Below threshold the signal passes through. Above threshold it is
// compressed toward max-out with a (tiny) slope, so loud bursts of
// polyphony are smoothly clamped rather than hard-clipped.
//
// Java works in int32 space; Go works in float64 where 1.0 maps to
// int16 32767. Config values (max-in, max-out, threshold) are loaded
// from src/main/resources/config/main-limiter-config.yaml on the Java
// side and hard-coded here in float-scaled form.
type Limiter struct {
	maxIn, maxOut, threshold float64
	slope                    float64
	window                   int

	// Monotonic deque of (sampleIndex, |value|) with strictly decreasing
	// values - the front is the running max over the lookahead window.
	deque  []limiterSample
	bypass bool
	idx    int64
}

type limiterSample struct {
	idx int64
	abs float64
}

// NewLimiter mirrors src/main/resources/config/main-limiter-config.yaml:
//
//	max-in    = 0x7fffffff   (full int32)
//	max-out   = 0x7fff       (int16 max)
//	threshold = 0x6fff       (256 below max-out)
//	delay-ms  = 200          (sliding max window)
//
// Values are scaled by 1/32767 so that Go-side 1.0 == int16 full-scale.
func NewLimiter(sampleRate int) *Limiter {
	const scale = 32767.0
	maxIn := float64(0x7fffffff) / scale
	maxOut := float64(0x7fff) / scale
	threshold := float64(0x6fff) / scale
	return &Limiter{
		maxIn:     maxIn,
		maxOut:    maxOut,
		threshold: threshold,
		slope:     (maxOut - threshold) / (maxIn - threshold),
		window:    sampleRate / 5, // 200 ms
		bypass:    true,
	}
}

// Apply takes one summed sample and returns the (possibly compressed)
// output. Faithful to the Java Limiter, including its asymmetric
// cold-start: while in bypass mode, only positive samples above
// threshold wake the tracker; once active, both polarities are tracked
// (the max-tracker stores |sum|).
func (l *Limiter) Apply(sum float64) float64 {
	l.idx++
	if l.bypass && sum < l.threshold {
		return sum
	}
	abs := sum
	if abs < 0 {
		abs = -abs
	}

	for len(l.deque) > 0 && l.deque[len(l.deque)-1].abs <= abs {
		l.deque = l.deque[:len(l.deque)-1]
	}
	l.deque = append(l.deque, limiterSample{l.idx, abs})

	cutoff := l.idx - int64(l.window)
	for len(l.deque) > 0 && l.deque[0].idx <= cutoff {
		l.deque = l.deque[1:]
	}

	if len(l.deque) == 0 {
		return sum
	}
	max := l.deque[0].abs
	if max < l.threshold {
		l.bypass = true
		l.deque = l.deque[:0]
		return sum
	}
	l.bypass = false
	adjusted := l.slope*(max-l.threshold) + l.threshold
	return (adjusted / max) * sum
}
