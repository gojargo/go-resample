package resample

import "math"

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
}

// Three sinc tiers. beta is chosen from the Kaiser stopband relation
// beta ≈ 0.1102*(A-8.7) for target attenuation A.
var (
	// Measured (see quality_test.go): SNR is upsample worst-case passband;
	// BW is the -3 dB point; float32 I/O caps measurable SNR near ~150 dB.
	sincFastest = sincParams{name: "fastest", zeroX: 10, l: 128, cutoff: 0.84, beta: 10.0} // ~97 dB SNR, ~78% BW
	sincMedium  = sincParams{name: "medium", zeroX: 16, l: 256, cutoff: 0.915, beta: 12.5} // ~123 dB SNR, ~87% BW
	sincBest    = sincParams{name: "best", zeroX: 40, l: 512, cutoff: 0.975, beta: 16.5}   // ~150+ dB SNR, ~95% BW
)

// sincKernel is a band-limited sinc interpolation kernel.
type sincKernel struct {
	coeffs       []float64 // prototype filter, index 0 = centre; length half+2
	coeffHalfLen int       // = zeroX * l (one-sided length in table entries)
	indexInc     float64   // = l (table entries per input sample at unity increment)
	zeroX        int
}

func newSincKernel(p sincParams) *sincKernel {
	return &sincKernel{
		coeffs:       designSinc(p.zeroX, p.l, p.cutoff, p.beta),
		coeffHalfLen: p.zeroX * p.l,
		indexInc:     float64(p.l),
		zeroX:        p.zeroX,
	}
}

func (s *sincKernel) maxHalfTaps() int { return s.zeroX }

func (s *sincKernel) support(ratioClamp float64) (int, int) {
	// Downsampling widens support to zeroX/ratio input frames per side.
	n := int(math.Ceil(float64(s.zeroX)/ratioClamp)) + 2
	return n, n
}

func (s *sincKernel) output(buf []float32, channels, base, c int, frac, ratioClamp float64) float64 {
	inc := s.indexInc * ratioClamp // table step per input sample
	coeffMax := float64(s.coeffHalfLen)
	n := len(buf)

	// Left half: input frames base, base-1, base-2, ... The filter is sampled at
	// distances frac, frac+1, frac+2, ... input samples from the interpolation
	// point, i.e. table positions frac*inc, frac*inc+inc, ...
	left := 0.0
	fi := frac * inc
	di := base*channels + c
	for fi < coeffMax {
		cf := s.lerp(fi)
		if di >= 0 && di < n {
			left += cf * float64(buf[di])
		}
		fi += inc
		di -= channels
	}

	// Right half: input frames base+1, base+2, ... at distances (1-frac),
	// (2-frac), ... from the interpolation point.
	right := 0.0
	fi = (1.0 - frac) * inc
	di = (base+1)*channels + c
	for fi < coeffMax {
		cf := s.lerp(fi)
		if di >= 0 && di < n {
			right += cf * float64(buf[di])
		}
		fi += inc
		di += channels
	}

	return ratioClamp * (left + right)
}

func (s *sincKernel) reset() {}

// lerp linearly interpolates the coefficient table at fractional index fi.
func (s *sincKernel) lerp(fi float64) float64 {
	i := int(fi)
	f := fi - float64(i)
	return s.coeffs[i] + f*(s.coeffs[i+1]-s.coeffs[i])
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
