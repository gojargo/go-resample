package main

import "math"

// analysisLen is the FFT/segment length used for the differential comparison.
const analysisLen = 1 << 15 // 32768

type metrics struct {
	lag         int     // integer sample delay of go relative to soxr
	fracTau     float64 // additional fractional-sample delay
	voiceNullDB float64 // residual in the voice band (<=~7kHz) — the number for speech
	fullNullDB  float64 // residual full-band — includes transition/stopband design diffs
	magDiffDB   float64 // worst passband magnitude difference, dB
	lenSoxr     int
	lenGo       int
}

// analyze compares two output signals (soxr = a, go = b), both at outRate. It
// aligns them (integer + fractional delay) and reports how close they are, both
// full-band and restricted to the voice band.
func analyze(a, b []float64, inRate, outRate int) metrics {
	m := metrics{lenSoxr: len(a), lenGo: len(b)}

	// Coarse integer-lag alignment via normalized cross-correlation.
	m.lag = bestLag(a, b, 4000)

	// Choose a centred analysis window valid for both signals given the lag.
	n := min(len(a), len(b))
	start := (n - analysisLen) / 2
	if start < 5000 {
		start = 5000
	}
	if start+m.lag < 0 {
		start = -m.lag
	}
	// Bounds guard.
	if start+analysisLen > len(a) || start+m.lag+analysisLen > len(b) || start < 0 {
		return m
	}
	as := a[start : start+analysisLen]
	bs := b[start+m.lag : start+m.lag+analysisLen]

	// Voice band: inside both signals' content and Nyquists, capped at 7 kHz.
	hiHz := math.Min(7000, 0.9*float64(min(inRate, outRate))/2)
	loBin := int(50.0 * analysisLen / float64(outRate))
	hiBin := int(hiHz * analysisLen / float64(outRate))
	if loBin < 1 {
		loBin = 1
	}

	m.magDiffDB = magDiff(as, bs, loBin, hiBin)

	// Fractional-delay alignment (minimizes full-band residual), then measure
	// the residual both full-band and restricted to the voice band.
	m.fracTau, m.fullNullDB = bestFracNull(as, bs)
	bAligned := fractionalShift(bs, m.fracTau)
	m.voiceNullDB = bandNull(as, bAligned, loBin, hiBin)
	return m
}

// bandNull returns the residual energy of (a-b) relative to a over [loBin,hiBin],
// via windowed FFTs (dB below in-band signal).
func bandNull(a, b []float64, loBin, hiBin int) float64 {
	ar, ai := hannFFT(a)
	br, bi := hannFFT(b)
	var res, ref float64
	for k := loBin; k <= hiBin; k++ {
		dr, di := ar[k]-br[k], ai[k]-bi[k]
		res += dr*dr + di*di
		ref += ar[k]*ar[k] + ai[k]*ai[k]
	}
	return 10 * math.Log10(res/(ref+1e-300)+1e-300)
}

// bestLag returns the integer lag of b relative to a maximizing normalized
// cross-correlation over a central window.
func bestLag(a, b []float64, maxLag int) int {
	n := min(len(a), len(b))
	w := n / 2
	c0 := n / 4
	best := math.Inf(-1)
	bestLag := 0
	for lag := -maxLag; lag <= maxLag; lag++ {
		var dot, ea, eb float64
		for i := c0; i < c0+w; i++ {
			j := i + lag
			if j < 0 || j >= len(b) {
				continue
			}
			dot += a[i] * b[j]
			ea += a[i] * a[i]
			eb += b[j] * b[j]
		}
		if ea == 0 || eb == 0 {
			continue
		}
		if c := dot / math.Sqrt(ea*eb); c > best {
			best = c
			bestLag = lag
		}
	}
	return bestLag
}

// magDiff returns the worst |20*log10(|A|/|B|)| over [loBin,hiBin], considering
// only bins with meaningful energy (delay-invariant: magnitude ignores phase).
func magDiff(a, b []float64, loBin, hiBin int) float64 {
	ar, ai := hannFFT(a)
	br, bi := hannFFT(b)
	// Reference peak to gate out noise-floor bins.
	var peak float64
	for k := loBin; k <= hiBin; k++ {
		if p := ar[k]*ar[k] + ai[k]*ai[k]; p > peak {
			peak = p
		}
	}
	thresh := peak * 1e-6 // ~ -60 dB relative to the loudest bin
	worst := 0.0
	for k := loBin; k <= hiBin; k++ {
		pa := ar[k]*ar[k] + ai[k]*ai[k]
		pb := br[k]*br[k] + bi[k]*bi[k]
		if pa < thresh || pb < thresh {
			continue
		}
		d := math.Abs(10 * math.Log10(pa/pb))
		if d > worst {
			worst = d
		}
	}
	return worst
}

// bestFracNull searches fractional delays tau in [-1,1] and returns the tau and
// the null residual (dB) at the best tau, over the central region (avoiding the
// circular-shift wrap at the edges).
func bestFracNull(a, b []float64) (float64, float64) {
	lo, hi := analysisLen/8, 7*analysisLen/8
	var refE float64
	for i := lo; i < hi; i++ {
		refE += a[i] * a[i]
	}
	bestTau, bestRes := 0.0, math.Inf(1)
	for tau := -1.0; tau <= 1.0001; tau += 0.02 {
		shifted := fractionalShift(b, tau)
		var res float64
		for i := lo; i < hi; i++ {
			d := a[i] - shifted[i]
			res += d * d
		}
		if res < bestRes {
			bestRes = res
			bestTau = tau
		}
	}
	nullDB := 10 * math.Log10(bestRes/refE+1e-300)
	return bestTau, nullDB
}

// fractionalShift delays x by tau samples via an FFT phase ramp (circular).
func fractionalShift(x []float64, tau float64) []float64 {
	n := len(x)
	re := make([]float64, n)
	im := make([]float64, n)
	copy(re, x)
	fftPow2(re, im)
	for k := 0; k < n; k++ {
		f := float64(k)
		if k > n/2 {
			f = float64(k - n)
		}
		ph := -2 * math.Pi * f * tau / float64(n)
		c, s := math.Cos(ph), math.Sin(ph)
		re[k], im[k] = re[k]*c-im[k]*s, re[k]*s+im[k]*c
	}
	ifftPow2(re, im)
	return re
}

// hannFFT windows x with a Hann window and returns its FFT.
func hannFFT(x []float64) ([]float64, []float64) {
	n := len(x)
	re := make([]float64, n)
	im := make([]float64, n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
		re[i] = x[i] * w
	}
	fftPow2(re, im)
	return re, im
}

// fftPow2 computes the in-place radix-2 FFT of (re, im); len must be power of 2.
func fftPow2(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		ang := -2 * math.Pi / float64(length)
		wlRe, wlIm := math.Cos(ang), math.Sin(ang)
		half := length >> 1
		for i := 0; i < n; i += length {
			wRe, wIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				aI, bI := i+k, i+k+half
				vRe := re[bI]*wRe - im[bI]*wIm
				vIm := re[bI]*wIm + im[bI]*wRe
				re[bI], im[bI] = re[aI]-vRe, im[aI]-vIm
				re[aI], im[aI] = re[aI]+vRe, im[aI]+vIm
				wRe, wIm = wRe*wlRe-wIm*wlIm, wRe*wlIm+wIm*wlRe
			}
		}
	}
}

// ifftPow2 computes the inverse FFT in place.
func ifftPow2(re, im []float64) {
	n := len(re)
	for i := range im {
		im[i] = -im[i]
	}
	fftPow2(re, im)
	inv := 1.0 / float64(n)
	for i := range re {
		re[i] *= inv
		im[i] = -im[i] * inv
	}
}
