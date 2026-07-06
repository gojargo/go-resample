package resample

import (
	"math"
	"testing"
)

func allConverters() []Converter {
	return []Converter{SincBestQuality, SincMediumQuality, SincFastest, ZeroOrderHold, Linear}
}

func TestIsValidRatio(t *testing.T) {
	cases := []struct {
		ratio float64
		want  bool
	}{
		{1.0, true},
		{2.0, true},
		{0.5, true},
		{256.0, true},
		{1.0 / 256.0, true},
		{257.0, false},
		{1.0 / 257.0, false},
		{0, false},
		{-1, false},
	}
	for _, c := range cases {
		if got := IsValidRatio(c.ratio); got != c.want {
			t.Errorf("IsValidRatio(%v) = %v, want %v", c.ratio, got, c.want)
		}
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(SincBestQuality, 0); err != ErrBadChannelCount {
		t.Errorf("New with 0 channels: got %v, want ErrBadChannelCount", err)
	}
	if _, err := New(Converter(99), 1); err != ErrBadConverter {
		t.Errorf("New with bad converter: got %v, want ErrBadConverter", err)
	}
	if _, err := New(SincBestQuality, 2); err != nil {
		t.Errorf("New valid: unexpected error %v", err)
	}
}

// TestOutputLength checks that Simple produces roughly ratio*inputFrames output.
func TestOutputLength(t *testing.T) {
	const inFrames = 4096
	in := make([]float32, inFrames)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 4 * float64(i) / inFrames))
	}
	for _, conv := range allConverters() {
		for _, ratio := range []float64{2.0, 0.5, 1.5, 48000.0 / 44100.0} {
			out, err := Simple(in, ratio, 1, conv)
			if err != nil {
				t.Fatalf("%v ratio %v: %v", conv, ratio, err)
			}
			want := int(float64(inFrames) * ratio)
			got := len(out)
			// Allow a modest margin for filter delay / edge handling.
			if math.Abs(float64(got-want)) > float64(want)*0.02+256 {
				t.Errorf("%v ratio %v: got %d frames, want ~%d", conv, ratio, got, want)
			}
		}
	}
}

// TestDCPreservation checks that a constant (DC) input maps to a constant output
// of the same value in steady state, for every converter.
func TestDCPreservation(t *testing.T) {
	const inFrames = 8192
	const level = 0.5
	in := make([]float32, inFrames)
	for i := range in {
		in[i] = level
	}
	for _, conv := range allConverters() {
		for _, ratio := range []float64{2.0, 0.5, 3.0, 1.0 / 3.0} {
			out, err := Simple(in, ratio, 1, conv)
			if err != nil {
				t.Fatalf("%v ratio %v: %v", conv, ratio, err)
			}
			// Examine the steady-state middle third, avoiding edge transients.
			lo, hi := len(out)/3, 2*len(out)/3
			if hi <= lo {
				t.Fatalf("%v ratio %v: output too short (%d)", conv, ratio, len(out))
			}
			var maxErr float64
			for i := lo; i < hi; i++ {
				if e := math.Abs(float64(out[i]) - level); e > maxErr {
					maxErr = e
				}
			}
			tol := 1e-4
			if conv == ZeroOrderHold || conv == Linear {
				tol = 1e-6 // these reproduce a constant exactly
			}
			if maxErr > tol {
				t.Errorf("%v ratio %v: DC not preserved, max error %g (tol %g)", conv, ratio, maxErr, tol)
			}
		}
	}
}

// TestSinePassband checks that a low-frequency sine passes through the sinc
// converters with amplitude close to unity (well inside the passband).
func TestSinePassband(t *testing.T) {
	const inFrames = 16384
	const freq = 20.0 // cycles over the whole buffer => very low, deep in passband
	in := make([]float32, inFrames)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / inFrames))
	}
	for _, conv := range []Converter{SincBestQuality, SincMediumQuality, SincFastest} {
		out, err := Simple(in, 2.0, 1, conv)
		if err != nil {
			t.Fatalf("%v: %v", conv, err)
		}
		lo, hi := len(out)/4, 3*len(out)/4
		var peak float64
		for i := lo; i < hi; i++ {
			if a := math.Abs(float64(out[i])); a > peak {
				peak = a
			}
		}
		if peak < 0.98 || peak > 1.02 {
			t.Errorf("%v: passband sine peak %.4f, want ~1.0", conv, peak)
		}
	}
}

// TestStreamingMatchesSimple checks that feeding the input in small chunks
// through Process yields (nearly) the same result as a single Simple call.
func TestStreamingMatchesSimple(t *testing.T) {
	const inFrames = 8192
	in := make([]float32, inFrames)
	for i := range in {
		in[i] = float32(0.3 * math.Sin(2*math.Pi*17*float64(i)/inFrames))
	}
	const ratio = 1.5

	oneShot, err := Simple(in, ratio, 1, SincMediumQuality)
	if err != nil {
		t.Fatal(err)
	}

	r, err := New(SincMediumQuality, 1)
	if err != nil {
		t.Fatal(err)
	}
	var streamed []float32
	const chunk = 137 // deliberately awkward chunk size
	outBuf := make([]float32, 4096)
	for off := 0; off < inFrames; off += chunk {
		end := min(off+chunk, inFrames)
		d := &Data{
			In:           in[off:end],
			Out:          outBuf,
			InputFrames:  end - off,
			OutputFrames: len(outBuf),
			Ratio:        ratio,
			EndOfInput:   end == inFrames,
		}
		if err := r.Process(d); err != nil {
			t.Fatal(err)
		}
		streamed = append(streamed, d.Out[:d.OutputFramesGen]...)
		// Drain any output still buffered internally for this chunk.
		for {
			d2 := &Data{In: nil, Out: outBuf, InputFrames: 0, OutputFrames: len(outBuf), Ratio: ratio, EndOfInput: end == inFrames}
			if err := r.Process(d2); err != nil {
				t.Fatal(err)
			}
			if d2.OutputFramesGen == 0 {
				break
			}
			streamed = append(streamed, d2.Out[:d2.OutputFramesGen]...)
		}
	}

	// Compare the overlapping steady-state region.
	n := min(len(oneShot), len(streamed))
	if n < inFrames { // should be ~1.5x inFrames
		t.Fatalf("streamed output too short: %d (oneShot %d)", len(streamed), len(oneShot))
	}
	lo, hi := n/4, 3*n/4
	var maxErr float64
	for i := lo; i < hi; i++ {
		if e := math.Abs(float64(oneShot[i]) - float64(streamed[i])); e > maxErr {
			maxErr = e
		}
	}
	if maxErr > 1e-5 {
		t.Errorf("streaming vs one-shot mismatch: max error %g", maxErr)
	}
}

func TestStereoInterleaving(t *testing.T) {
	const inFrames = 4096
	in := make([]float32, inFrames*2)
	for i := 0; i < inFrames; i++ {
		in[2*i] = 0.5    // left: constant
		in[2*i+1] = -0.5 // right: constant
	}
	out, err := Simple(in, 2.0, 2, SincMediumQuality)
	if err != nil {
		t.Fatal(err)
	}
	frames := len(out) / 2
	lo, hi := frames/3, 2*frames/3
	for i := lo; i < hi; i++ {
		l, r := float64(out[2*i]), float64(out[2*i+1])
		if math.Abs(l-0.5) > 1e-4 || math.Abs(r-(-0.5)) > 1e-4 {
			t.Fatalf("frame %d: channels not preserved/independent: L=%g R=%g", i, l, r)
			break
		}
	}
}
