package resample

import (
	"errors"
	"math"
	"sync"
	"testing"
)

// testSignal returns frames*channels interleaved samples: a different mix of
// tones per channel, bounded by 0.5 in magnitude.
func testSignal(frames, channels int) []float32 {
	s := make([]float32, frames*channels)
	for n := 0; n < frames; n++ {
		for c := 0; c < channels; c++ {
			f := 0.013 * float64(c+1)
			s[n*channels+c] = float32(0.3*math.Sin(2*math.Pi*f*float64(n)) + 0.2*math.Sin(2*math.Pi*0.21*float64(n)+float64(c)))
		}
	}
	return s
}

// streamAll pushes in through r in chunks of chunk frames, using an output
// buffer of outCap frames and draining after every chunk, and returns all
// output produced.
func streamAll(t testing.TB, r *Resampler, in []float32, ratio float64, chunk, outCap int) []float32 {
	t.Helper()
	ch := r.Channels()
	frames := len(in) / ch
	out := make([]float32, outCap*ch)
	var got []float32
	for off := 0; off < frames; off += chunk {
		end := min(off+chunk, frames)
		eoi := end == frames
		d := &Data{In: in[off*ch : end*ch], InputFrames: end - off, Out: out, OutputFrames: outCap, Ratio: ratio, EndOfInput: eoi}
		for {
			if err := r.Process(d); err != nil {
				t.Fatal(err)
			}
			if d.InputFramesUsed != d.InputFrames {
				t.Fatalf("InputFramesUsed = %d, want %d", d.InputFramesUsed, d.InputFrames)
			}
			if d.OutputFramesGen < 0 || d.OutputFramesGen > d.OutputFrames {
				t.Fatalf("OutputFramesGen = %d outside [0, %d]", d.OutputFramesGen, d.OutputFrames)
			}
			got = append(got, out[:d.OutputFramesGen*ch]...)
			if d.OutputFramesGen == 0 {
				break
			}
			// Drain whatever did not fit before feeding more input.
			d = &Data{In: nil, InputFrames: 0, Out: out, OutputFrames: outCap, Ratio: ratio, EndOfInput: eoi}
		}
	}
	return got
}

func TestConverterString(t *testing.T) {
	want := map[Converter]string{
		SincBestQuality:   "SincBestQuality",
		SincMediumQuality: "SincMediumQuality",
		SincFastest:       "SincFastest",
		ZeroOrderHold:     "ZeroOrderHold",
		Linear:            "Linear",
		Converter(-1):     "UnknownConverter",
		Converter(42):     "UnknownConverter",
	}
	for c, s := range want {
		if got := c.String(); got != s {
			t.Errorf("Converter(%d).String() = %q, want %q", int(c), got, s)
		}
	}
}

func TestAccessors(t *testing.T) {
	for _, conv := range allConverters() {
		r, err := New(conv, 3)
		if err != nil {
			t.Fatal(err)
		}
		if r.Converter() != conv {
			t.Errorf("Converter() = %v, want %v", r.Converter(), conv)
		}
		if r.Channels() != 3 {
			t.Errorf("Channels() = %d, want 3", r.Channels())
		}
	}
}

func TestProcessValidation(t *testing.T) {
	r, err := New(Linear, 2)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]float32, 20)  // 10 frames
	out := make([]float32, 40) // 20 frames
	valid := func() *Data {
		return &Data{In: in, InputFrames: 10, Out: out, OutputFrames: 20, Ratio: 1}
	}
	cases := []struct {
		name string
		mod  func(d *Data) *Data
		want error
	}{
		{"nil Data", func(*Data) *Data { return nil }, ErrBadData},
		{"nil Out", func(d *Data) *Data { d.Out = nil; return d }, ErrBadData},
		{"zero ratio", func(d *Data) *Data { d.Ratio = 0; return d }, ErrBadRatio},
		{"ratio too high", func(d *Data) *Data { d.Ratio = MaxRatio * 1.01; return d }, ErrBadRatio},
		{"ratio too low", func(d *Data) *Data { d.Ratio = MinRatio * 0.99; return d }, ErrBadRatio},
		{"NaN ratio", func(d *Data) *Data { d.Ratio = math.NaN(); return d }, ErrBadRatio},
		{"Inf ratio", func(d *Data) *Data { d.Ratio = math.Inf(1); return d }, ErrBadRatio},
		{"negative InputFrames", func(d *Data) *Data { d.InputFrames = -1; return d }, ErrBadData},
		{"negative OutputFrames", func(d *Data) *Data { d.OutputFrames = -1; return d }, ErrBadData},
		{"InputFrames exceeds In", func(d *Data) *Data { d.InputFrames = 11; return d }, ErrBadData},
		{"OutputFrames exceeds Out", func(d *Data) *Data { d.OutputFrames = 21; return d }, ErrBadData},
		{"valid", func(d *Data) *Data { return d }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := r.Process(c.mod(valid())); !errors.Is(err, c.want) {
				t.Errorf("Process: got %v, want %v", err, c.want)
			}
		})
	}
}

func TestSimpleValidation(t *testing.T) {
	in := make([]float32, 10)
	if _, err := Simple(in, 1, 0, Linear); err != ErrBadChannelCount {
		t.Errorf("channels=0: got %v, want ErrBadChannelCount", err)
	}
	if _, err := Simple(in, 0, 1, Linear); err != ErrBadRatio {
		t.Errorf("ratio=0: got %v, want ErrBadRatio", err)
	}
	if _, err := Simple(in, 1, 3, Linear); err != ErrBadData {
		t.Errorf("len(in) not a multiple of channels: got %v, want ErrBadData", err)
	}
	if _, err := Simple(in, 1, 1, Converter(42)); err != ErrBadConverter {
		t.Errorf("bad converter: got %v, want ErrBadConverter", err)
	}
}

func TestSimpleEmptyInput(t *testing.T) {
	for _, conv := range allConverters() {
		for _, in := range [][]float32{nil, {}} {
			out, err := Simple(in, 2, 1, conv)
			if err != nil {
				t.Fatalf("%v: %v", conv, err)
			}
			if len(out) != 0 {
				t.Errorf("%v: empty input produced %d samples", conv, len(out))
			}
		}
	}
}

// TestResetReproducible checks that after Reset a Resampler produces exactly
// the same output as a fresh one, including after a ratio change.
func TestResetReproducible(t *testing.T) {
	for _, conv := range allConverters() {
		for _, ch := range []int{1, 2, 3} {
			in := testSignal(2000, ch)
			fresh, err := New(conv, ch)
			if err != nil {
				t.Fatal(err)
			}
			want := streamAll(t, fresh, in, 1.3, 250, 300)

			r, _ := New(conv, ch)
			_ = streamAll(t, r, in, 0.7, 333, 50) // dirty state with a different ratio
			if err := r.SetRatio(3); err != nil {
				t.Fatal(err)
			}
			r.Reset()
			got := streamAll(t, r, in, 1.3, 250, 300)

			if len(got) != len(want) {
				t.Fatalf("%v ch=%d: after Reset got %d samples, want %d", conv, ch, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%v ch=%d: sample %d differs after Reset: %g vs %g", conv, ch, i, got[i], want[i])
				}
			}
		}
	}
}

// rampInput returns the mono signal x[n] = n. Linear interpolation of it yields
// the exact fractional read position, which makes ratio behaviour observable.
func rampInput(frames int) []float32 {
	s := make([]float32, frames)
	for i := range s {
		s[i] = float32(i)
	}
	return s
}

func TestSetRatio(t *testing.T) {
	r, err := New(Linear, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []float64{0, -1, MaxRatio * 2, math.NaN()} {
		if err := r.SetRatio(bad); err != ErrBadRatio {
			t.Errorf("SetRatio(%v): got %v, want ErrBadRatio", bad, err)
		}
	}

	in := rampInput(400)
	out := make([]float32, 1000)

	// Block 1 at ratio 1, then step to ratio 2 with SetRatio: block 2 must be
	// spaced by exactly 1/2 input sample from its very first output.
	d := &Data{In: in[:200], InputFrames: 200, Out: out, OutputFrames: 199, Ratio: 1}
	if err := r.Process(d); err != nil {
		t.Fatal(err)
	}
	last := out[d.OutputFramesGen-1]
	if err := r.SetRatio(2); err != nil {
		t.Fatal(err)
	}
	d = &Data{In: in[200:], InputFrames: 200, Out: out, OutputFrames: 300, Ratio: 2}
	if err := r.Process(d); err != nil {
		t.Fatal(err)
	}
	if d.OutputFramesGen < 100 {
		t.Fatalf("block 2 produced only %d frames", d.OutputFramesGen)
	}
	// The advance to block 2's first output was taken at the old ratio when
	// block 1 finished, so the step to out[0] is 1; from there on it is 1/2.
	if step := out[0] - last; math.Abs(float64(step)-1) > 1e-3 {
		t.Fatalf("step into block 2 = %g, want 1", step)
	}
	prev := out[0]
	for i := 1; i < d.OutputFramesGen; i++ {
		if step := out[i] - prev; math.Abs(float64(step)-0.5) > 1e-3 {
			t.Fatalf("output %d: step %g after SetRatio(2), want 0.5", i, step)
		}
		prev = out[i]
	}
}

func TestRatioRamp(t *testing.T) {
	r, err := New(Linear, 1)
	if err != nil {
		t.Fatal(err)
	}
	in := rampInput(400)
	out := make([]float32, 1000)

	d := &Data{In: in[:200], InputFrames: 200, Out: out, OutputFrames: 199, Ratio: 1}
	if err := r.Process(d); err != nil {
		t.Fatal(err)
	}
	prev := out[d.OutputFramesGen-1]

	// Ratio 1 -> 2 over 200 output frames: the step between outputs must shrink
	// monotonically from ~1 towards 1/2.
	d = &Data{In: in[200:], InputFrames: 200, Out: out, OutputFrames: 200, Ratio: 2}
	if err := r.Process(d); err != nil {
		t.Fatal(err)
	}
	if d.OutputFramesGen < 150 {
		t.Fatalf("ramp block produced only %d frames", d.OutputFramesGen)
	}
	var steps []float64
	for i := 0; i < d.OutputFramesGen; i++ {
		steps = append(steps, float64(out[i]-prev))
		prev = out[i]
	}
	if math.Abs(steps[0]-1) > 1e-3 {
		t.Errorf("first step %g, want ~1 (ramp should start at the previous ratio)", steps[0])
	}
	for i := 1; i < len(steps); i++ {
		if steps[i] > steps[i-1]+1e-3 {
			t.Fatalf("step %d grew from %g to %g; ramp should be monotonic", i, steps[i-1], steps[i])
		}
	}
	if final := steps[len(steps)-1]; final > 0.75 {
		t.Errorf("last step %g; ramp towards 1/2 barely progressed", final)
	}
}

// TestStreamingMatchesSimpleAll checks, for every converter, several ratios and
// channel counts and awkward chunk / output-buffer sizes (down to one frame),
// that streaming produces the same signal as a one-shot Simple call.
func TestStreamingMatchesSimpleAll(t *testing.T) {
	ratios := []float64{2, 0.5, 48000.0 / 44100.0, 16000.0 / 48000.0, 3.7}
	shapes := []struct{ chunk, outCap int }{{137, 4096}, {64, 1}, {1, 7}, {1000, 333}}
	for _, conv := range allConverters() {
		for _, ch := range []int{1, 2, 3} {
			in := testSignal(1500, ch)
			for _, ratio := range ratios {
				want, err := Simple(in, ratio, ch, conv)
				if err != nil {
					t.Fatal(err)
				}
				for _, sh := range shapes {
					r, _ := New(conv, ch)
					got := streamAll(t, r, in, ratio, sh.chunk, sh.outCap)
					// Accumulated read positions can round differently across
					// chunk boundaries, so allow one frame of length slack.
					if d := len(got) - len(want); d < -ch || d > ch {
						t.Errorf("%v ch=%d ratio=%.3f %+v: got %d frames, want %d", conv, ch, ratio, sh, len(got)/ch, len(want)/ch)
						continue
					}
					n := min(len(got), len(want))
					for i := 0; i < n; i++ {
						if e := math.Abs(float64(got[i] - want[i])); e > 1e-5 {
							t.Errorf("%v ch=%d ratio=%.3f %+v: sample %d differs by %g", conv, ch, ratio, sh, i, e)
							break
						}
					}
				}
			}
		}
	}
}

// TestMultichannelMatchesMono checks that each channel of a stereo or
// multichannel conversion matches converting that channel alone, i.e. channels
// are independent and every kernel code path (mono, stereo, generic) agrees.
// The paths perform the same operations in the same order, so on amd64 they
// are bit-identical; the tolerance only absorbs last-bit differences where the
// compiler fuses multiply-adds (arm64, GOAMD64=v3).
func TestMultichannelMatchesMono(t *testing.T) {
	for _, conv := range allConverters() {
		for _, ch := range []int{2, 3, 5} {
			for _, ratio := range []float64{48000.0 / 44100.0, 0.37} {
				in := testSignal(1200, ch)
				multi, err := Simple(in, ratio, ch, conv)
				if err != nil {
					t.Fatal(err)
				}
				for c := 0; c < ch; c++ {
					mono := make([]float32, len(in)/ch)
					for n := range mono {
						mono[n] = in[n*ch+c]
					}
					single, err := Simple(mono, ratio, 1, conv)
					if err != nil {
						t.Fatal(err)
					}
					if len(single)*ch != len(multi) {
						t.Fatalf("%v ch=%d: mono %d frames vs multichannel %d", conv, ch, len(single), len(multi)/ch)
					}
					for n := range single {
						if math.Abs(float64(single[n]-multi[n*ch+c])) > 1e-6 {
							t.Fatalf("%v ch=%d ratio=%.3f: channel %d frame %d: %g (multi) vs %g (mono)",
								conv, ch, ratio, c, n, multi[n*ch+c], single[n])
						}
					}
				}
			}
		}
	}
}

// TestExtremeRatios exercises the ratio limits: output length must track the
// ratio and every sample must be finite and bounded.
func TestExtremeRatios(t *testing.T) {
	for _, conv := range allConverters() {
		for _, tc := range []struct {
			ratio  float64
			frames int
		}{{MaxRatio, 300}, {MinRatio, 20000}} {
			in := testSignal(tc.frames, 2)
			out, err := Simple(in, tc.ratio, 2, conv)
			if err != nil {
				t.Fatalf("%v ratio %v: %v", conv, tc.ratio, err)
			}
			want := float64(tc.frames) * tc.ratio
			if got := float64(len(out) / 2); math.Abs(got-want) > 2 {
				t.Errorf("%v ratio %v: %v frames, want ~%v", conv, tc.ratio, got, want)
			}
			for i, v := range out {
				if math.IsNaN(float64(v)) || math.Abs(float64(v)) > 1 {
					t.Fatalf("%v ratio %v: sample %d = %g (input bounded by 0.5)", conv, tc.ratio, i, v)
				}
			}
		}
	}
}

// TestConcurrentResamplers runs many Resamplers of the same converter in
// parallel. They share the read-only filter tables, so run with -race.
func TestConcurrentResamplers(t *testing.T) {
	in := testSignal(2000, 2)
	for _, conv := range []Converter{SincFastest, SincMediumQuality, SincBestQuality} {
		want, err := Simple(in, 48000.0/44100.0, 2, conv)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan string, 8)
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := Simple(in, 48000.0/44100.0, 2, conv)
				if err != nil || len(got) != len(want) {
					errs <- "length/error mismatch"
					return
				}
				for i := range got {
					if got[i] != want[i] {
						errs <- "sample mismatch"
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Errorf("%v: concurrent run: %s", conv, e)
		}
	}
}
