package main

import (
	"fmt"
	"math"

	resample "github.com/gojargo/go-resample"
)

// runDebug isolates the source of the downsampling discrepancy.
func runDebug() {
	// (1) Pure passband sines: if these null cleanly, the passband path is fine
	// and any broadband discrepancy is stopband/high-frequency handling.
	fmt.Println("== pure sine (isolates passband) ==")
	for _, tc := range []struct {
		in, out int
		freq    float64
	}{
		{48000, 16000, 1000},
		{48000, 16000, 3000},
		{48000, 16000, 6000},
		{24000, 48000, 1000},
	} {
		sig := genSine(tc.in, durationSec, tc.freq)
		sox := toF64(runSoxr(tc.in, tc.out, sig))
		g := toF64(runGo(tc.in, tc.out, sig, resample.SincBestQuality))
		m := analyze(sox, g, tc.in, tc.out)
		fmt.Printf("  sine %5.0fHz  %d->%d  lag=%d frac=%.3f  voiceNull=%.1f dB  fullNull=%.1f dB  mag=%.3f dB\n",
			tc.freq, tc.in, tc.out, m.lag, m.fracTau, m.voiceNullDB, m.fullNullDB, m.magDiffDB)
	}

	// (2) Broadband 48k->16k: integer-lag scan + where the residual concentrates.
	fmt.Println("== broadband 48k->16k (locate the residual) ==")
	sig := genSignal(48000, durationSec)
	sox := toF64(runSoxr(48000, 16000, sig))
	g := toF64(runGo(48000, 16000, sig, resample.SincBestQuality))

	bestRes, bestLagScan := math.Inf(1), 0
	for lag := -60; lag <= 60; lag++ {
		if r := residualAtLag(sox, g, lag); r < bestRes {
			bestRes, bestLagScan = r, lag
		}
	}
	fmt.Printf("  bestLag()=%d   scan-bestLag=%d   null@scanBest=%.1f dB\n",
		bestLag(sox, g, 4000), bestLagScan, 10*math.Log10(bestRes+1e-300))

	// Residual by time-eighth at the scan-best lag (find the hot region).
	printResidualByEighth(sox, g, bestLagScan)

	// (3) Where is the chirp at each eighth? (to correlate with stopband).
	fmt.Println("  (chirp sweeps 100Hz -> 21.6kHz over 4s; output Nyquist = 8kHz)")
}

func residualAtLag(a, b []float64, lag int) float64 {
	n := min(len(a), len(b))
	lo, hi := n/4, 3*n/4
	var res, ref float64
	for i := lo; i < hi; i++ {
		j := i + lag
		if j < 0 || j >= len(b) {
			continue
		}
		d := a[i] - b[j]
		res += d * d
		ref += a[i] * a[i]
	}
	return res / (ref + 1e-300)
}

func printResidualByEighth(a, b []float64, lag int) {
	n := min(len(a), len(b))
	for e := 0; e < 8; e++ {
		lo := n * e / 8
		hi := n * (e + 1) / 8
		var res, ref float64
		for i := lo; i < hi; i++ {
			j := i + lag
			if j < 0 || j >= len(b) {
				continue
			}
			d := a[i] - b[j]
			res += d * d
			ref += a[i] * a[i]
		}
		fmt.Printf("    eighth %d/8: null=%.1f dB  (signal energy %.3f)\n",
			e+1, 10*math.Log10(res/(ref+1e-300)+1e-300), ref/float64(hi-lo))
	}
}

func genSine(rate int, seconds, freq float64) []int16 {
	n := int(float64(rate) * seconds)
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(math.Round(0.7 * 32767 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))))
	}
	return out
}
