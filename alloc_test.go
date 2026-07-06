package resample

import (
	"math"
	"testing"
)

// TestProcessZeroAlloc verifies the streaming path performs no heap allocations
// once its internal buffer has reached the steady-state working-set size. This
// is the property that makes Process safe for real-time audio callbacks.
func TestProcessZeroAlloc(t *testing.T) {
	converters := []Converter{Linear, ZeroOrderHold, SincFastest, SincMediumQuality, SincBestQuality}
	for _, conv := range converters {
		t.Run(conv.String(), func(t *testing.T) {
			const (
				channels = 2
				frames   = 480 // 10 ms @ 48 kHz
				ratio    = 48000.0 / 44100.0
			)
			r, err := New(conv, channels)
			if err != nil {
				t.Fatal(err)
			}
			in := make([]float32, frames*channels)
			for i := range in {
				in[i] = float32(0.2 * math.Sin(float64(i)*0.1))
			}
			out := make([]float32, frames*channels*4)
			d := &Data{
				In:           in,
				Out:          out,
				InputFrames:  frames,
				OutputFrames: len(out) / channels,
				Ratio:        ratio,
			}
			// Warm up so the internal buffer reaches its steady-state capacity.
			for i := 0; i < 16; i++ {
				if err := r.Process(d); err != nil {
					t.Fatal(err)
				}
			}
			allocs := testing.AllocsPerRun(500, func() {
				if err := r.Process(d); err != nil {
					t.Fatal(err)
				}
			})
			if allocs != 0 {
				t.Errorf("%v: Process allocated %.2f objects/call in steady state, want 0", conv, allocs)
			}
		})
	}
}
