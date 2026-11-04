package resample

import (
	"math"
	"sync"
)

// Precomputed-phase ("direct") evaluation of the sinc filters.
//
// When the conversion ratio is a fraction with a small denominator, which is
// the case for every standard pair of audio rates (44.1k->48k is 160/147,
// 16k->48k is 3/1, 48k->16k is 1/3), the read position only ever takes
// `phases` distinct fractional values j/phases. Following the "direct" mode of
// the Speex resampler, the filter coefficients for each of those phases are
// computed once, and the read position is tracked exactly as an integer frame
// plus a phase index, so no per-tap table interpolation is needed.
//
// The coefficients are taken from the same interpolated prototype table, with
// the same arithmetic, as the general path, so both paths evaluate the same
// filter: output differs from the general path only by float64 rounding.
// Tables are shared, read-only, by every Resampler that needs them.

const (
	// maxPhaseTableCoeffs caps the size of one phase table (1 MiB of float64).
	// Ratios whose table would be larger use the general path.
	maxPhaseTableCoeffs = 1 << 17
	// maxPhaseCacheCoeffs caps the total size of all cached tables (16 MiB).
	maxPhaseCacheCoeffs = 1 << 21
)

// phaseTable holds per-phase filter coefficients for one sinc preset and
// clamped ratio. left[j][k] weights input frame base-k and right[j][k] weights
// frame base+1+k for an output at position base + j/phases.
type phaseTable struct {
	phases int
	left   [][]float64
	right  [][]float64
}

type phaseKey struct {
	p      *sincParams
	phases int
	rc     float64
}

// usePhaseTables enables precomputed-phase evaluation; tests turn it off to
// compare against the general path.
var usePhaseTables = true

var phaseCache struct {
	sync.Mutex
	tables map[phaseKey]*phaseTable
	coeffs int
}

// ratioPhases returns the smallest phase count n such that one output advances
// the read position by exactly step/n input frames (step an integer), or ok ==
// false if there is none with n <= maxPhases.
func ratioPhases(ratio float64, maxPhases int) (n, step int, ok bool) {
	adv := 1 / ratio // input frames per output frame
	for n := 1; n <= maxPhases; n++ {
		x := adv * float64(n)
		r := math.Round(x)
		if r >= 1 && math.Abs(x-r) <= 1e-9*r {
			return n, int(r), true
		}
	}
	return 0, 0, false
}

// phaseTableFor returns the shared phase table for preset p at the given ratio,
// building it on first use, or nil if the ratio is not a suitable fraction or
// the table would exceed the size limits.
func phaseTableFor(p *sincParams, ratio float64) (t *phaseTable, step int) {
	if !usePhaseTables {
		return nil, 0
	}
	rc := math.Min(ratio, 1)
	// Taps per phase (both halves) bound the number of phases that fit.
	taps := 2 * (int(math.Ceil(float64(p.zeroX)/rc)) + 1)
	phases, step, ok := ratioPhases(ratio, min(maxPhaseTableCoeffs/taps, 4096))
	if !ok {
		return nil, 0
	}
	key := phaseKey{p: p, phases: phases, rc: rc}

	phaseCache.Lock()
	defer phaseCache.Unlock()
	if t := phaseCache.tables[key]; t != nil {
		return t, step
	}
	if phaseCache.coeffs+phases*taps > maxPhaseCacheCoeffs {
		return nil, 0
	}
	t = buildPhaseTable(p.coeffs(), p.zeroX*p.l, float64(p.l)*rc, phases)
	if phaseCache.tables == nil {
		phaseCache.tables = make(map[phaseKey]*phaseTable)
	}
	phaseCache.tables[key] = t
	phaseCache.coeffs += phases * taps
	return t, step
}

// buildPhaseTable evaluates the interpolated prototype exactly as
// sincKernel.output does, for frac = j/phases. All phases share one backing
// array, sized up front.
func buildPhaseTable(coeffs []float64, coeffHalfLen int, inc float64, phases int) *phaseTable {
	coeffMax := float64(coeffHalfLen)
	// Taps in a half starting at table position fi0 (same loop condition as
	// the general path, so the counts match it exactly).
	count := func(fi float64) int {
		n := 0
		for ; fi < coeffMax; fi += inc {
			n++
		}
		return n
	}
	fill := func(dst []float64, fi float64) {
		for k := range dst {
			i := int(fi)
			c0 := coeffs[i]
			dst[k] = c0 + (fi-float64(i))*(coeffs[i+1]-c0)
			fi += inc
		}
	}
	t := &phaseTable{phases: phases, left: make([][]float64, phases), right: make([][]float64, phases)}
	starts := make([][2]float64, phases)
	total := 0
	for j := range starts {
		frac := float64(j) / float64(phases)
		starts[j] = [2]float64{frac * inc, (1.0 - frac) * inc}
		total += count(starts[j][0]) + count(starts[j][1])
	}
	flat := make([]float64, total)
	for j, st := range starts {
		n := count(st[0])
		t.left[j], flat = flat[:n:n], flat[n:]
		fill(t.left[j], st[0])
		n = count(st[1])
		t.right[j], flat = flat[:n:n], flat[n:]
		fill(t.right[j], st[1])
	}
	return t
}

// outputPhase computes one output frame at position base + j/t.phases from a
// phase table. Taps outside buf are treated as zeros, as in output.
func (s *sincKernel) outputPhase(buf []float32, channels, base, j int, rc float64, dst []float32) {
	t := s.pt
	lc, rcf := t.left[j], t.right[j]
	frames := len(buf) / channels
	// Left taps reach back to frame 0 at most; right taps up to the last frame.
	lc = lc[:min(len(lc), base+1)]
	rcf = rcf[:max(0, min(len(rcf), frames-base-1))]

	switch channels {
	case 1:
		left, right := 0.0, 0.0
		in := buf[:base+1]
		for k, c := range lc {
			left += c * float64(in[base-k])
		}
		in = buf[base+1:]
		for k, c := range rcf {
			right += c * float64(in[k])
		}
		dst[0] = float32(rc * (left + right))
	case 2:
		var l0, l1, r0, r1 float64
		for k, c := range lc {
			f := 2 * (base - k)
			l0 += c * float64(buf[f])
			l1 += c * float64(buf[f+1])
		}
		for k, c := range rcf {
			f := 2 * (base + 1 + k)
			r0 += c * float64(buf[f])
			r1 += c * float64(buf[f+1])
		}
		dst[0] = float32(rc * (l0 + r0))
		dst[1] = float32(rc * (l1 + r1))
	default:
		left := s.acc[:channels]
		right := s.acc[channels : 2*channels]
		clear(s.acc)
		for k, c := range lc {
			frame := buf[(base-k)*channels : (base-k+1)*channels]
			for ch, x := range frame {
				left[ch] += c * float64(x)
			}
		}
		for k, c := range rcf {
			frame := buf[(base+1+k)*channels : (base+2+k)*channels]
			for ch, x := range frame {
				right[ch] += c * float64(x)
			}
		}
		for ch := range dst[:channels] {
			dst[ch] = float32(rc * (left[ch] + right[ch]))
		}
	}
}
