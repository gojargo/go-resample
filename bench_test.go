package resample

import (
	"math"
	"testing"
)

// Benchmarks process 20 ms stereo blocks through a warmed-up Resampler, the
// typical shape of a real-time audio callback. SetBytes counts input bytes, so
// MB/s is input throughput; "x-realtime" is how many times faster than real time
// a single stream at the input rate runs on one core.

func benchmarkProcess(b *testing.B, conv Converter, inRate, outRate int) {
	benchmarkProcessCh(b, conv, inRate, outRate, 2)
}

func benchmarkProcessCh(b *testing.B, conv Converter, inRate, outRate, channels int) {
	frames := inRate / 50 // 20 ms
	ratio := float64(outRate) / float64(inRate)

	r, err := New(conv, channels)
	if err != nil {
		b.Fatal(err)
	}
	in := make([]float32, frames*channels)
	for i := range in {
		in[i] = float32(0.5 * math.Sin(float64(i/channels)*0.05))
	}
	outFrames := int(float64(frames)*ratio) + 64
	out := make([]float32, outFrames*channels)
	d := &Data{In: in, Out: out, InputFrames: frames, OutputFrames: outFrames, Ratio: ratio}
	for i := 0; i < 8; i++ { // warm up to the steady-state working set
		if err := r.Process(d); err != nil {
			b.Fatal(err)
		}
	}

	b.SetBytes(int64(len(in) * 4))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Process(d); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if s := b.Elapsed().Seconds(); s > 0 {
		b.ReportMetric(float64(b.N)*0.020/s, "x-realtime")
	}
}

func BenchmarkProcess(b *testing.B) {
	flows := []struct {
		name            string
		inRate, outRate int
	}{
		{"44k1to48k", 44100, 48000},
		{"16kto48k", 16000, 48000},
		{"48kto16k", 48000, 16000},
	}
	for _, conv := range allConverters() {
		for _, f := range flows {
			b.Run(conv.String()+"/"+f.name, func(b *testing.B) {
				benchmarkProcess(b, conv, f.inRate, f.outRate)
			})
		}
	}
}

func BenchmarkNew(b *testing.B) {
	for _, conv := range allConverters() {
		b.Run(conv.String(), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := New(conv, 2); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
