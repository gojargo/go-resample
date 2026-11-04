package resample

import (
	"math"
	"sync"
)

// sincParams describes a band-limited sinc converter preset.
//
// The prototype low-pass filter is a Kaiser-windowed sinc, densely sampled into
// a table with l entries per input sample (the "phases"). At run time the
// filter is evaluated by stepping through the table at a rate proportional to
// the (clamped) conversion ratio, with linear interpolation between adjacent
// table entries. Downsampling (ratio < 1) shrinks the step, which widens the filter
// in the input domain and lowers its cutoff, providing anti-aliasing; the
// output is scaled by the ratio to keep unity gain.
type sincParams struct {
	name   string
	zeroX  int     // one-sided zero crossings (half-taps at unity increment)
	l      int     // table phases per input sample (oversampling of the filter)
	cutoff float64 // cutoff as a fraction of Nyquist, in (0, 1]
	beta   float64 // Kaiser window beta (larger => deeper stopband, wider transition)

	once  sync.Once
	table []float64 // designed lazily, then shared read-only by every Resampler
}

// coeffs returns the preset's prototype filter, designing it on first use. The
// table is immutable after construction, so concurrent Resamplers share it.
func (p *sincParams) coeffs() []float64 {
	p.once.Do(func() { p.table = designSinc(p.zeroX, p.l, p.cutoff, p.beta) })
	return p.table
}

// Three sinc tiers. beta is chosen from the Kaiser stopband relation
// beta ≈ 0.1102*(A-8.7) for target attenuation A.
var (
	// Measured (see quality_test.go): SNR is upsample worst-case passband;
	// BW is the -3 dB point; float32 I/O caps measurable SNR near ~150 dB.
	sincFastest = &sincParams{name: "fastest", zeroX: 10, l: 128, cutoff: 0.84, beta: 10.0} // ~97 dB SNR, ~78% BW
	sincMedium  = &sincParams{name: "medium", zeroX: 16, l: 256, cutoff: 0.915, beta: 12.5} // ~123 dB SNR, ~87% BW
	sincBest    = &sincParams{name: "best", zeroX: 40, l: 512, cutoff: 0.975, beta: 16.5}   // ~150+ dB SNR, ~95% BW
)

// sincKernel is a band-limited sinc interpolation kernel.
type sincKernel struct {
	coeffs       []float64 // shared prototype filter, index 0 = centre; length half+2
	coeffHalfLen int       // = zeroX * l (one-sided length in table entries)
	indexInc     float64   // = l (table entries per input sample at unity increment)
	zeroX        int
	acc          []float64 // per-channel accumulators (2*channels), reused per frame
	params       *sincParams

	// Precomputed-phase state (see phase.go), set by preparePhases.
	pt        *phaseTable
	ptRatio   float64 // ratio pt was built for
	phaseStep int     // phase advance per output frame
}

// preparePhases looks up (building if needed) the phase table for ratio. It
// reports whether direct evaluation is available at that ratio.
func (s *sincKernel) preparePhases(ratio float64) bool {
	if s.pt != nil && s.ptRatio == ratio {
		return true
	}
	t, step := phaseTableFor(s.params, ratio)
	s.pt, s.ptRatio, s.phaseStep = t, ratio, step
	return t != nil
}

func newSincKernel(p *sincParams, channels int) *sincKernel {
	return &sincKernel{
		coeffs:       p.coeffs(),
		coeffHalfLen: p.zeroX * p.l,
		indexInc:     float64(p.l),
		zeroX:        p.zeroX,
		acc:          make([]float64, 2*channels),
		params:       p,
	}
}

func (s *sincKernel) support(ratioClamp float64) (int, int) {
	// Downsampling widens support to zeroX/ratio input frames per side.
	n := int(math.Ceil(float64(s.zeroX)/ratioClamp)) + 2
	return n, n
}

// output evaluates the filter once per tap and applies each coefficient to every
// channel of the corresponding input frame. Taps that fall outside buf
// (before the stream start or past the end of available input) are treated as
// zeros and skipped entirely; they are always at the far end of each half, so
// the loops simply stop early.
func (s *sincKernel) output(buf []float32, channels, base int, frac, ratioClamp float64, dst []float32) {
	inc := s.indexInc * ratioClamp // table step per input sample
	coeffMax := float64(s.coeffHalfLen)
	coeffs := s.coeffs
	frames := len(buf) / channels

	if channels == 1 {
		// Left half: input frames base, base-1, ... at distances frac, frac+1, ...
		// from the interpolation point, i.e. table positions frac*inc, frac*inc+inc, ...
		left := 0.0
		fi := frac * inc
		for f := base; f >= 0 && fi < coeffMax; f-- {
			i := int(fi)
			c0 := coeffs[i]
			left += (c0 + (fi-float64(i))*(coeffs[i+1]-c0)) * float64(buf[f])
			fi += inc
		}
		// Right half: input frames base+1, base+2, ... at distances (1-frac),
		// (2-frac), ...
		right := 0.0
		fi = (1.0 - frac) * inc
		for f := base + 1; f < frames && fi < coeffMax; f++ {
			i := int(fi)
			c0 := coeffs[i]
			right += (c0 + (fi-float64(i))*(coeffs[i+1]-c0)) * float64(buf[f])
			fi += inc
		}
		dst[0] = float32(ratioClamp * (left + right))
		return
	}

	if channels == 2 {
		// Stereo: same arithmetic as the generic path, with the four running
		// sums kept in registers instead of the shared accumulator slice.
		var l0, l1, r0, r1 float64
		fi := frac * inc
		for f := base; f >= 0 && fi < coeffMax; f-- {
			i := int(fi)
			c0 := coeffs[i]
			cf := c0 + (fi-float64(i))*(coeffs[i+1]-c0)
			frame := buf[2*f : 2*f+2]
			l0 += cf * float64(frame[0])
			l1 += cf * float64(frame[1])
			fi += inc
		}
		fi = (1.0 - frac) * inc
		for f := base + 1; f < frames && fi < coeffMax; f++ {
			i := int(fi)
			c0 := coeffs[i]
			cf := c0 + (fi-float64(i))*(coeffs[i+1]-c0)
			frame := buf[2*f : 2*f+2]
			r0 += cf * float64(frame[0])
			r1 += cf * float64(frame[1])
			fi += inc
		}
		dst[0] = float32(ratioClamp * (l0 + r0))
		dst[1] = float32(ratioClamp * (l1 + r1))
		return
	}

	// Generic multichannel path.
	left := s.acc[:channels]
	right := s.acc[channels : 2*channels]
	clear(s.acc)

	fi := frac * inc
	for f := base; f >= 0 && fi < coeffMax; f-- {
		i := int(fi)
		c0 := coeffs[i]
		cf := c0 + (fi-float64(i))*(coeffs[i+1]-c0)
		frame := buf[f*channels : f*channels+channels]
		for c, x := range frame {
			left[c] += cf * float64(x)
		}
		fi += inc
	}

	fi = (1.0 - frac) * inc
	for f := base + 1; f < frames && fi < coeffMax; f++ {
		i := int(fi)
		c0 := coeffs[i]
		cf := c0 + (fi-float64(i))*(coeffs[i+1]-c0)
		frame := buf[f*channels : f*channels+channels]
		for c, x := range frame {
			right[c] += cf * float64(x)
		}
		fi += inc
	}

	for c := range dst[:channels] {
		dst[c] = float32(ratioClamp * (left[c] + right[c]))
	}
}

// designSinc builds a Kaiser-windowed sinc prototype filter, normalised for
// unity DC gain. It returns half+2 coefficients (index 0 = centre) with two
// trailing zeros so lerp can always read coeffs[i+1].
func designSinc(zeroX, l int, cutoff, beta float64) []float64 {
	half := zeroX * l
	g := make([]float64, half+2)
	ibeta := i0(beta)
	invHalf := 1.0 / float64(half)
	for i := 0; i <= half; i++ {
		x := float64(i) / float64(l) // distance from centre in input samples
		// Kaiser window over the finite support [-half, half].
		t := float64(i) * invHalf
		w := i0(beta*math.Sqrt(1-t*t)) / ibeta
		g[i] = sincPi(cutoff*x) * w
	}
	// Normalise so DC gain (sum of the taps that land on integer input offsets)
	// is exactly 1 at zero fractional phase.
	dc := g[0]
	for k := l; k <= half; k += l {
		dc += 2 * g[k]
	}
	inv := 1.0 / dc
	for i := range g {
		g[i] *= inv
	}
	return g
}

// sincPi is the normalised sinc, sin(pi*x)/(pi*x), with sincPi(0)=1.
func sincPi(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// i0 is the zeroth-order modified Bessel function of the first kind, evaluated
// by its power series. Used for the Kaiser window.
func i0(x float64) float64 {
	sum := 1.0
	term := 1.0
	half := x / 2
	for k := 1; k < 64; k++ {
		r := half / float64(k)
		term *= r * r
		sum += term
		if term < sum*1e-17 {
			break
		}
	}
	return sum
}
