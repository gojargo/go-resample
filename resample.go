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
// safe for concurrent use; use one Resampler per stream. Distinct Resamplers
// may run concurrently: they share only immutable filter tables, which are
// built once per converter on first use, so New is cheap.
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
		k = newSincKernel(sincFastest, channels)
	case SincMediumQuality:
		k = newSincKernel(sincMedium, channels)
	case SincBestQuality:
		k = newSincKernel(sincBest, channels)
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
// contract.
//
// When d.Ratio differs from the previous call's ratio, the ratio is ramped
// linearly from the old value to the new one, giving smooth speed-up /
// slow-down effects. The ramp is spread over d.OutputFrames (the capacity of
// Out), not over the frames actually produced: if Out is much larger than the
// output a call generates, the ramp only partly progresses before the next
// call starts from d.Ratio. Size Out close to the expected output (about
// InputFrames*Ratio) for smooth ramps, or use [Resampler.SetRatio] for an
// immediate step change. This matches libsamplerate's semantics.
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
	// Exact output length is ceil(inFrames*ratio); the headroom only guards
	// against rounding. The loop below grows the buffer if it is ever short.
	outFrames := int(math.Ceil(float64(inFrames)*ratio)) + 16
	out := make([]float32, outFrames*channels)
	gen := 0

	// Feed the input in chunks rather than all at once: this bounds the
	// Resampler's internal history (instead of copying the whole input into it)
	// and keeps the fractional read position small and precise.
	for off := 0; ; {
		end := min(off+simpleChunkFrames, inFrames)
		d := &Data{
			In:          in[off*channels : end*channels],
			InputFrames: end - off,
			Ratio:       ratio,
			EndOfInput:  end == inFrames,
		}
		for {
			if gen == outFrames {
				grow := outFrames/4 + 64
				out = append(out, make([]float32, grow*channels)...)
				outFrames += grow
			}
			d.Out = out[gen*channels:]
			d.OutputFrames = outFrames - gen
			if err := r.Process(d); err != nil {
				return nil, err
			}
			gen += d.OutputFramesGen
			if gen < outFrames {
				break // Process stopped for lack of input, not of output space.
			}
			d.In, d.InputFrames = nil, 0 // drain what did not fit
		}
		if end == inFrames {
			break
		}
		off = end
	}
	return out[:gen*channels], nil
}

// simpleChunkFrames is the input block size Simple feeds to Process.
const simpleChunkFrames = 1 << 14

// positionSnap is the distance, in input samples, within which the accumulated
// read position is snapped to the nearest integer. Advancing by 1/ratio is
// rarely exact in binary (1/3, 44100/48000, ...), so without snapping rounding
// error builds up and positions that should land exactly on an input sample
// fall just short of it, e.g. emitting 48001 frames for one second of 16 kHz
// audio upsampled to 48 kHz. Snapping resets the error every time an exact
// position is reached; 1e-8 samples (~0.2 ps at 48 kHz) is far below audibility
// and well above the accumulated float64 error.
const positionSnap = 1e-8

func snapPosition(p float64) float64 {
	if r := math.Round(p); math.Abs(p-r) < positionSnap {
		return r
	}
	return p
}

// kernel is the per-algorithm interpolation core. Implementations are stateless
// with respect to the sample stream; all stream state lives in streamState.
type kernel interface {
	// support returns how many input frames of context the kernel needs to the
	// left and right of the current sample position, for a given clamped ratio
	// (min(ratio, 1)). Downsampling widens the effective filter, so support may
	// grow as the ratio shrinks.
	support(ratioClamp float64) (left, right int)
	// output computes one output frame at frame index base with fractional
	// offset frac in [0,1) and writes its channels to dst[:channels].
	// ratioClamp is min(ratio, 1). base is always in [0, len(buf)/channels).
	output(buf []float32, channels, base int, frac, ratioClamp float64, dst []float32)
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
		r.kernel.output(buf, ch, base, frac, rc, d.Out[outGen:outGen+ch])
		outGen += ch
		st.inPos = snapPosition(st.inPos + 1.0/ratio)
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
