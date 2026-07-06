package resample

import (
	"fmt"
	"math"
	"testing"
)

// This file is an objective-quality harness for the sinc converters. It measures
// two things with an FFT of the resampled output:
//
//   - SNR (dB): signal power vs. everything else (distortion + aliasing +
//     imaging + noise) for a pure tone placed in the passband.
//   - Gain (dB): output/input amplitude, which in the passband shows flatness
//     and in the stopband is the alias-rejection depth.
//
// Frequencies are normalised so the input rate is 1.0 (input Nyquist = 0.5).
// A 7-term Blackman-Harris window (~ -180 dB sidelobes) keeps spectral leakage
// well below the float32 I/O noise floor (~ -140 dB), so deep stopbands are
// measurable. A centred analysis window avoids the filter's edge transients.

const fftSize = 1 << 14

// fftPow2 computes the in-place radix-2 FFT of (re, im); len must be a power of 2.
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
				a := i + k
				b := a + half
				vRe := re[b]*wRe - im[b]*wIm
				vIm := re[b]*wIm + im[b]*wRe
				re[b], im[b] = re[a]-vRe, im[a]-vIm
				re[a], im[a] = re[a]+vRe, im[a]+vIm
				wRe, wIm = wRe*wlRe-wIm*wlIm, wRe*wlIm+wIm*wlRe
			}
		}
	}
}

// bh7 is a 7-term Blackman-Harris window value at sample n of N.
func bh7(n, N int) float64 {
	const (
		a0 = 0.27105140069342
		a1 = 0.43329793923448
		a2 = 0.21812299954311
		a3 = 0.06592544638803
		a4 = 0.01081174209837
		a5 = 0.00077658482522
		a6 = 0.00001388721735
	)
	x := 2 * math.Pi * float64(n) / float64(N)
	return a0 - a1*math.Cos(x) + a2*math.Cos(2*x) - a3*math.Cos(3*x) +
		a4*math.Cos(4*x) - a5*math.Cos(5*x) + a6*math.Cos(6*x)
}

// measureAt resamples a pure sine of normalised input frequency f0 (cycles per
// input sample, 0 < f0 < 0.5 and f0 < 0.5*ratio) and returns the SNR and gain in
// dB measured on a centred, windowed segment of the output.
func measureAt(conv Converter, ratio, f0 float64) (snrDB, gainDB float64) {
	outNeeded := fftSize * 3
	nIn := int(float64(outNeeded)/ratio) + 8192
	in := make([]float32, nIn)
	for n := range in {
		in[n] = float32(math.Sin(2 * math.Pi * f0 * float64(n)))
	}
	out, err := Simple(in, ratio, 1, conv)
	if err != nil || len(out) < fftSize {
		return math.NaN(), math.NaN()
	}
	start := (len(out) - fftSize) / 2
	seg := out[start : start+fftSize]

	// Gain from unwindowed RMS (input sine RMS = 1/sqrt2).
	var sum2 float64
	for _, v := range seg {
		sum2 += float64(v) * float64(v)
	}
	outRMS := math.Sqrt(sum2 / float64(fftSize))
	gainDB = 20 * math.Log10(outRMS/math.Sqrt2*2+1e-300) // = 20*log10(outRMS/(1/sqrt2))

	// SNR from windowed spectrum.
	re := make([]float64, fftSize)
	im := make([]float64, fftSize)
	for n := 0; n < fftSize; n++ {
		re[n] = float64(seg[n]) * bh7(n, fftSize)
	}
	fftPow2(re, im)

	pow := func(k int) float64 {
		k = ((k % fftSize) + fftSize) % fftSize
		return re[k]*re[k] + im[k]*im[k]
	}
	fOut := f0 / ratio
	sigBin := int(math.Round(fOut * float64(fftSize)))
	const guard = 16

	var total float64
	for k := 0; k < fftSize; k++ {
		total += re[k]*re[k] + im[k]*im[k]
	}
	seen := make(map[int]bool, 8*guard)
	collect := func(center int) float64 {
		var s float64
		for k := center - guard; k <= center+guard; k++ {
			kk := ((k % fftSize) + fftSize) % fftSize
			if !seen[kk] {
				seen[kk] = true
				s += pow(kk)
			}
		}
		return s
	}
	dc := collect(0)
	sig := collect(sigBin) + collect(fftSize-sigBin)
	noise := total - sig - dc
	if noise < 1e-300 {
		noise = 1e-300
	}
	snrDB = 10 * math.Log10(sig/noise)
	return snrDB, gainDB
}

// TestSincQualityReport prints a table of measured SNR / passband gain /
// stopband rejection for each sinc tier. Run with `go test -run Report -v`.
func TestSincQualityReport(t *testing.T) {
	tiers := []Converter{SincFastest, SincMediumQuality, SincBestQuality}
	ratios := []float64{2.0, 0.5}

	for _, ratio := range ratios {
		nyq := 0.5 * math.Min(1, ratio) // output-referred usable band (input-sample units)
		t.Logf("──────── ratio %.3f  (usable passband up to f0=%.3f) ────────", ratio, nyq)
		t.Logf("%-18s %14s %14s %16s %16s", "converter", "min passband", "passband gain", "stopband rej.", "-3dB bandwidth")
		for _, conv := range tiers {
			// Passband SNR at several fractions of the usable band.
			minSNR := math.Inf(1)
			for _, frac := range []float64{0.1, 0.25, 0.5, 0.75} {
				snr, _ := measureAt(conv, ratio, frac*nyq)
				if snr < minSNR {
					minSNR = snr
				}
			}
			// Passband gain flatness: worst deviation from 0 dB up to 0.5*nyq.
			var worstGain float64
			for _, frac := range []float64{0.1, 0.3, 0.5} {
				_, g := measureAt(conv, ratio, frac*nyq)
				if math.Abs(g) > math.Abs(worstGain) {
					worstGain = g
				}
			}
			// Stopband rejection: only meaningful when downsampling (ratio<1),
			// where frequencies in (nyq, 0.5) must be rejected.
			stop := math.NaN()
			if ratio < 1 {
				stop = math.Inf(-1)
				for _, f0 := range []float64{nyq * 1.15, nyq * 1.4, nyq * 1.7, 0.48} {
					if f0 >= 0.5 {
						continue
					}
					_, g := measureAt(conv, ratio, f0)
					if g > stop { // least attenuation = worst case
						stop = g
					}
				}
			}
			// -3 dB bandwidth: first frequency where gain drops below -3 dB.
			bw := findBandwidth(conv, ratio, nyq)
			stopStr := "     n/a"
			if ratio < 1 {
				stopStr = formatDB(stop)
			}
			t.Logf("%-18s %11.1f dB %11.2f dB %13s dB %13.1f%%", conv, minSNR, worstGain, stopStr, bw*100)
		}
	}
}

// findBandwidth returns the -3 dB point as a fraction of the usable Nyquist.
func findBandwidth(conv Converter, ratio, nyq float64) float64 {
	for frac := 0.99; frac > 0.05; frac -= 0.01 {
		_, g := measureAt(conv, ratio, frac*nyq)
		if g > -3.0 {
			return frac
		}
	}
	return 0
}

func formatDB(v float64) string {
	if math.IsInf(v, -1) || v < -400 {
		return "  <-300"
	}
	return fmt.Sprintf("%.1f", -v) // report rejection as a positive attenuation
}

// TestSincQualityThresholds asserts the tiers meet ordered, stable minimums.
// Thresholds are conservative floors, not the typical measured values.
func TestSincQualityThresholds(t *testing.T) {
	type want struct {
		minPassbandSNR float64 // dB, worst case across passband
		minStopbandRej float64 // dB attenuation, downsample worst case
	}
	cases := map[Converter]want{
		SincFastest:       {minPassbandSNR: 80, minStopbandRej: 70},
		SincMediumQuality: {minPassbandSNR: 105, minStopbandRej: 82},
		SincBestQuality:   {minPassbandSNR: 125, minStopbandRej: 120},
	}
	for conv, w := range cases {
		// Passband SNR (worst case), upsample and downsample.
		for _, ratio := range []float64{2.0, 0.5} {
			nyq := 0.5 * math.Min(1, ratio)
			minSNR := math.Inf(1)
			for _, frac := range []float64{0.1, 0.25, 0.5, 0.75} {
				snr, _ := measureAt(conv, ratio, frac*nyq)
				if snr < minSNR {
					minSNR = snr
				}
			}
			if minSNR < w.minPassbandSNR {
				t.Errorf("%v ratio %.2f: passband SNR %.1f dB < %.0f dB floor", conv, ratio, minSNR, w.minPassbandSNR)
			}
		}
		// Stopband rejection (downsample worst case).
		nyq := 0.25
		worst := math.Inf(-1)
		for _, f0 := range []float64{nyq * 1.15, nyq * 1.4, nyq * 1.7, 0.48} {
			_, g := measureAt(conv, 0.5, f0)
			if g > worst {
				worst = g
			}
		}
		if -worst < w.minStopbandRej {
			t.Errorf("%v: stopband rejection %.1f dB < %.0f dB floor", conv, -worst, w.minStopbandRej)
		}
	}
}
