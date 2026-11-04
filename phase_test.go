package resample

import (
	"math"
	"testing"
)

func TestRatioPhases(t *testing.T) {
	cases := []struct {
		ratio       float64
		phases, adv int
		ok          bool
	}{
		{48000.0 / 44100.0, 160, 147, true},
		{44100.0 / 48000.0, 147, 160, true},
		{3, 3, 1, true},
		{16000.0 / 48000.0, 1, 3, true},
		{2, 2, 1, true},
		{1, 1, 1, true},
		{MaxRatio, 256, 1, true},
		{MinRatio, 1, 256, true},
		{1.0001234, 0, 0, false}, // drift-compensation style: no small fraction
		{math.Pi, 0, 0, false},
	}
	for _, c := range cases {
		n, adv, ok := ratioPhases(c.ratio, 4096)
		if ok != c.ok || (ok && (n != c.phases || adv != c.adv)) {
			t.Errorf("ratioPhases(%v) = %d, %d, %v; want %d, %d, %v", c.ratio, n, adv, ok, c.phases, c.adv, c.ok)
		}
	}
}

// withPhaseTables runs f with precomputed phases enabled or disabled.
func withPhaseTables(enabled bool, f func()) {
	prev := usePhaseTables
	usePhaseTables = enabled
	defer func() { usePhaseTables = prev }()
	f()
}

// TestPhaseTablesMatchGeneralPath checks that precomputed-phase evaluation
// produces the same signal as the general interpolating path, for one-shot and
// streaming use, including ratio ramps, SetRatio and Reset. Both evaluate the
// same filter; the tolerance covers float64 rounding, which can decide whether
// a tap lying exactly on the window edge (where the Kaiser window is ~1e-5,
// not 0) is included.
func TestPhaseTablesMatchGeneralPath(t *testing.T) {
	ratios := []float64{48000.0 / 44100.0, 44100.0 / 48000.0, 3, 16000.0 / 48000.0, 0.37, 1.0001234, 48000.0 / 8000.0}
	type run func(conv Converter, ch int, ratio float64, in []float32) []float32
	runs := map[string]run{
		"Simple": func(conv Converter, ch int, ratio float64, in []float32) []float32 {
			out, err := Simple(in, ratio, ch, conv)
			if err != nil {
				t.Fatal(err)
			}
			return out
		},
		"stream tiny buffers": func(conv Converter, ch int, ratio float64, in []float32) []float32 {
			r, _ := New(conv, ch)
			return streamAll(t, r, in, ratio, 61, 5)
		},
		"ramp then constant": func(conv Converter, ch int, ratio float64, in []float32) []float32 {
			r, _ := New(conv, ch)
			n := len(in) / ch
			// Dirty the state at another ratio, then Reset: the first block
			// runs at 1.1*ratio and the rest ramp to and hold at ratio.
			_ = streamAll(t, r, in[:n/3*ch], math.Min(ratio*1.1, MaxRatio), 100, 200)
			r.Reset()
			out := make([]float32, 4096*ch)
			var got []float32
			for i, off := 0, 0; off < n; i, off = i+1, off+200 {
				end := min(off+200, n)
				ri := ratio
				if i == 0 {
					ri = math.Min(ratio*1.1, MaxRatio)
				}
				d := &Data{In: in[off*ch : end*ch], InputFrames: end - off, Out: out, OutputFrames: 4096, Ratio: ri, EndOfInput: end == n}
				if err := r.Process(d); err != nil {
					t.Fatal(err)
				}
				got = append(got, out[:d.OutputFramesGen*ch]...)
			}
			return got
		},
		"SetRatio": func(conv Converter, ch int, ratio float64, in []float32) []float32 {
			r, _ := New(conv, ch)
			if err := r.SetRatio(ratio); err != nil {
				t.Fatal(err)
			}
			return streamAll(t, r, in, ratio, 333, 1000)
		},
	}
	for _, conv := range []Converter{SincFastest, SincMediumQuality, SincBestQuality} {
		for _, ch := range []int{1, 2, 3} {
			in := testSignal(2500, ch)
			for _, ratio := range ratios {
				for name, f := range runs {
					var fast, general []float32
					withPhaseTables(true, func() { fast = f(conv, ch, ratio, in) })
					withPhaseTables(false, func() { general = f(conv, ch, ratio, in) })
					if len(fast) != len(general) {
						t.Errorf("%v ch=%d ratio=%.4f %s: %d vs %d frames", conv, ch, ratio, name, len(fast)/ch, len(general)/ch)
						continue
					}
					for i := range fast {
						if e := math.Abs(float64(fast[i] - general[i])); e > 1e-5 {
							t.Errorf("%v ch=%d ratio=%.4f %s: sample %d differs by %g", conv, ch, ratio, name, i, e)
							break
						}
					}
				}
			}
		}
	}
}

// TestPhaseTablesEngage checks that standard rate pairs actually take the
// precomputed path, and that a non-fractional ratio does not.
func TestPhaseTablesEngage(t *testing.T) {
	for _, tc := range []struct {
		ratio float64
		want  bool
	}{
		{48000.0 / 44100.0, true},
		{16000.0 / 48000.0, true},
		{1.0001234, false},
	} {
		r, err := New(SincMediumQuality, 2)
		if err != nil {
			t.Fatal(err)
		}
		in := testSignal(1000, 2)
		out := make([]float32, 4000)
		d := &Data{In: in, InputFrames: 1000, Out: out, OutputFrames: 2000, Ratio: tc.ratio}
		if err := r.Process(d); err != nil {
			t.Fatal(err)
		}
		if got := r.state.exact; got != tc.want {
			t.Errorf("ratio %v: precomputed path engaged = %v, want %v", tc.ratio, got, tc.want)
		}
	}
}

// TestPhaseExactLength checks that with exact phase stepping, the output
// length is exactly ceil(inputFrames * ratio) for standard rate pairs.
func TestPhaseExactLength(t *testing.T) {
	for _, conv := range []Converter{SincFastest, SincMediumQuality, SincBestQuality} {
		for _, f := range [][2]int{{44100, 48000}, {48000, 44100}, {16000, 48000}, {48000, 16000}, {8000, 44100}} {
			in := make([]float32, f[0]) // one second
			out, err := Simple(in, float64(f[1])/float64(f[0]), 1, conv)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != f[1] {
				t.Errorf("%v %d->%d: %d frames for one second, want %d", conv, f[0], f[1], len(out), f[1])
			}
		}
	}
}
