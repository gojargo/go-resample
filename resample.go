package resample

import "math"

// Ratio limits for a valid conversion.
const (
	MinRatio = 1.0 / 256.0
	MaxRatio = 256.0
)

// Converter selects the resampling algorithm, trading quality against CPU cost.
type Converter int

const (
	// SincBestQuality is a band-limited sinc interpolator with the widest
	// passband and deepest stopband. Highest CPU cost.
	SincBestQuality Converter = iota
	// SincMediumQuality is a band-limited sinc interpolator that is transparent
	// for virtually all real-world audio at moderate cost.
	SincMediumQuality
	// SincFastest is a band-limited sinc interpolator tuned for low CPU cost.
	SincFastest
	// ZeroOrderHold repeats the previous input sample. Trivial cost, low quality.
	ZeroOrderHold
	// Linear linearly interpolates between input samples. Trivial cost.
	Linear
)

// String returns the converter name.
func (c Converter) String() string {
	switch c {
	case SincBestQuality:
		return "SincBestQuality"
	case SincMediumQuality:
		return "SincMediumQuality"
	case SincFastest:
		return "SincFastest"
	case ZeroOrderHold:
		return "ZeroOrderHold"
	case Linear:
		return "Linear"
	default:
		return "UnknownConverter"
	}
}

// IsValidRatio reports whether ratio is within the supported range [1/256, 256].
func IsValidRatio(ratio float64) bool {
	return ratio >= MinRatio && ratio <= MaxRatio
}

// Data carries the input and output buffers and parameters for one call to
// [Resampler.Process]. Buffers hold interleaved float32 frames (one sample per
// channel per frame).
//
// The caller sets In, InputFrames, Out, OutputFrames, Ratio and EndOfInput.
// Process sets InputFramesUsed and OutputFramesGen.
type Data struct {
	// In holds the input frames: len(In) must be >= InputFrames*channels.
	In []float32
	// Out is the destination buffer: len(Out) must be >= OutputFrames*channels.
	Out []float32
	// InputFrames is the number of input frames available in In.
	InputFrames int
	// OutputFrames is the capacity, in frames, of Out.
	OutputFrames int
	// Ratio is output_rate / input_rate for this call.
	Ratio float64
	// EndOfInput must be set on the final call so trailing samples are flushed.
	EndOfInput bool

	// InputFramesUsed is set by Process: input frames consumed from In.
	InputFramesUsed int
	// OutputFramesGen is set by Process: output frames written to Out.
	OutputFramesGen int
}

// Resampler holds the state of a streaming sample-rate conversion. It is not
// safe for concurrent use; use one Resampler per stream.
type Resampler struct {
	conv      Converter
	channels  int
	kernel    kernel
	state     streamState
	lastRatio float64
	started   bool
}

// New creates a Resampler for the given converter and channel count.
func New(c Converter, channels int) (*Resampler, error) {
	if channels < 1 {
		return nil, ErrBadChannelCount
	}
	var k kernel
	switch c {
	case Linear:
		k = linearKernel{}
	case ZeroOrderHold:
		k = zohKernel{}
	case SincFastest:
		k = newSincKernel(sincFastest)
	case SincMediumQuality:
		k = newSincKernel(sincMedium)
	case SincBestQuality:
		k = newSincKernel(sincBest)
	default:
		return nil, ErrBadConverter
	}
	return &Resampler{conv: c, channels: channels, kernel: k}, nil
}

// Converter returns the converter this Resampler was created with.
func (r *Resampler) Converter() Converter { return r.conv }

// Channels returns the channel count.
func (r *Resampler) Channels() int { return r.channels }

// Reset clears all internal state, returning the Resampler to its initial
// condition as if freshly created.
func (r *Resampler) Reset() {
	r.state.reset()
	r.kernel.reset()
	r.lastRatio = 0
	r.started = false
}

// SetRatio forces an immediate (step, not ramped) change of the conversion
// ratio for the next Process call.
func (r *Resampler) SetRatio(ratio float64) error {
	if !IsValidRatio(ratio) {
		return ErrBadRatio
	}
	r.lastRatio = ratio
	r.started = true
	return nil
}

// Process performs a streaming conversion step. See [Data] for the calling
// contract. Within a call the ratio is linearly ramped from the ratio of the
// previous call to d.Ratio, giving smooth speed-up / slow-down effects.
//
// Note: Process absorbs all InputFrames of d.In into an internal buffer, so
// InputFramesUsed always equals InputFrames. To drain output that did not fit
// in Out, call again with InputFrames == 0.
//
// Once the internal buffer has grown to the steady-state working-set size,
// Process performs no heap allocations.
func (r *Resampler) Process(d *Data) error {
	if d == nil || d.Out == nil {
		return ErrBadData
	}
	if !IsValidRatio(d.Ratio) {
		return ErrBadRatio
	}
	if d.InputFrames < 0 || d.OutputFrames < 0 {
		return ErrBadData
	}
	if d.InputFrames*r.channels > len(d.In) {
		return ErrBadData
	}
	if d.OutputFrames*r.channels > len(d.Out) {
		return ErrBadData
	}
	if !r.started {
		// No ramp on the very first block.
		r.lastRatio = d.Ratio
		r.started = true
	}
	r.streamProcess(d)
	return nil
}

// Simple performs a one-shot conversion of an entire input buffer and returns
// the freshly allocated output.
func Simple(in []float32, ratio float64, channels int, c Converter) ([]float32, error) {
	if channels < 1 {
		return nil, ErrBadChannelCount
	}
	if !IsValidRatio(ratio) {
		return nil, ErrBadRatio
	}
	if len(in)%channels != 0 {
		return nil, ErrBadData
	}
	r, err := New(c, channels)
	if err != nil {
		return nil, err
	}
	inFrames := len(in) / channels
	// Output size estimate with headroom for filter delay and rounding.
	outFrames := int(float64(inFrames)*ratio) + 2*r.kernel.maxHalfTaps() + 16
	out := make([]float32, outFrames*channels)
	d := &Data{
		In:           in,
		Out:          out,
		InputFrames:  inFrames,
		OutputFrames: outFrames,
		Ratio:        ratio,
		EndOfInput:   true,
	}
	if err := r.Process(d); err != nil {
		return nil, err
	}
	return out[:d.OutputFramesGen*channels], nil
}

// kernel is the per-algorithm interpolation core. Implementations are stateless
// with respect to the sample stream; all stream state lives in streamState.
type kernel interface {
	// support returns how many input frames of context the kernel needs to the
	// left and right of the current sample position, for a given clamped ratio
	// (min(ratio, 1)). Downsampling widens the effective filter, so support may
	// grow as the ratio shrinks.
	support(ratioClamp float64) (left, right int)
	// output computes one output sample for channel c at frame index base with
	// fractional offset frac in [0,1). ratioClamp is min(ratio, 1).
	output(buf []float32, channels, base, c int, frac, ratioClamp float64) float64
	// maxHalfTaps returns the largest one-sided support in input frames, used to
	// size output estimates and priming.
	maxHalfTaps() int
	reset()
}

// streamState holds the sample-stream bookkeeping shared by all kernels: a
// history buffer of retained input (which also doubles as the reusable working
// buffer) and the fractional read position of the next output within it.
type streamState struct {
	history []float32
	inPos   float64
	primed  bool
}

func (s *streamState) reset() {
	s.history = s.history[:0]
	s.inPos = 0
	s.primed = false
}

// streamProcess is the shared engine driving every kernel.
func (r *Resampler) streamProcess(d *Data) {
	ch := r.channels
	st := &r.state

	// Append the new input onto the retained history in place. The history slice
	// doubles as the working buffer: append reuses its spare capacity, so once
	// the working set stabilises this does not allocate (no per-call make).
	inN := d.InputFrames * ch
	buf := append(st.history, d.In[:inN]...)
	st.history = buf
	bufFrames := len(buf) / ch

	if !st.primed {
		st.inPos = 0
		st.primed = true
	}

	outCap := d.OutputFrames * ch
	outGen := 0
	last := r.lastRatio
	target := d.Ratio
	ratio := target

	for outGen < outCap {
		// Linearly ramp the ratio across the requested output block.
		if d.OutputFrames > 0 {
			ratio = last + float64(outGen/ch)*(target-last)/float64(d.OutputFrames)
		}
		rc := ratio
		if rc > 1 {
			rc = 1
		}
		_, right := r.kernel.support(rc)
		base := int(math.Floor(st.inPos))

		if base+right >= bufFrames {
			if !d.EndOfInput {
				break // need more input to have right-hand context
			}
			if base >= bufFrames {
				break // consumed all input; nothing left to interpolate from
			}
			// EndOfInput: generate with zero right-context (buf bounds guard).
		}

		frac := st.inPos - float64(base)
		for c := 0; c < ch; c++ {
			d.Out[outGen+c] = float32(r.kernel.output(buf, ch, base, c, frac, rc))
		}
		outGen += ch
		st.inPos += 1.0 / ratio
	}

	// Trim history: keep enough left-context for the next output position.
	rcNext := target
	if rcNext > 1 {
		rcNext = 1
	}
	left, _ := r.kernel.support(rcNext)
	nextBase := int(math.Floor(st.inPos))
	dropTo := nextBase - left
	if dropTo < 0 {
		dropTo = 0
	}
	if dropTo > bufFrames {
		dropTo = bufFrames
	}
	// Compact in place: slide the retained tail down to the front of the same
	// backing array (copy handles the overlap), keeping the buffer bounded and
	// its capacity available for reuse next call. No allocation.
	if dropTo > 0 {
		m := copy(buf, buf[dropTo*ch:])
		st.history = buf[:m]
		st.inPos -= float64(dropTo)
	}

	d.InputFramesUsed = d.InputFrames
	d.OutputFramesGen = outGen / ch
	r.lastRatio = target
}
