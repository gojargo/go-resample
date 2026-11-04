package resample_test

import (
	"fmt"
	"math"

	resample "github.com/gojargo/go-resample"
)

// sine returns frames interleaved stereo frames of a 440 Hz tone at rate Hz.
func sine(frames, rate int) []float32 {
	s := make([]float32, 2*frames)
	for n := 0; n < frames; n++ {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(n)/float64(rate)))
		s[2*n], s[2*n+1] = v, v
	}
	return s
}

// Convert one second of 44.1 kHz stereo audio to 48 kHz in a single call.
func ExampleSimple() {
	in := sine(44100, 44100)

	out, err := resample.Simple(in, 48000.0/44100.0, 2, resample.SincMediumQuality)
	if err != nil {
		panic(err)
	}
	fmt.Println("output frames:", len(out)/2)
	// Output: output frames: 48000
}

// Stream 16 kHz mono audio to 48 kHz in 20 ms blocks, as in a real-time audio
// pipeline. Each call consumes the whole input block; set EndOfInput on the
// last block to flush the tail.
func ExampleResampler_Process() {
	const (
		inRate, outRate = 16000, 48000
		block           = inRate / 50 // 20 ms
		ratio           = float64(outRate) / inRate
	)
	r, err := resample.New(resample.SincFastest, 1)
	if err != nil {
		panic(err)
	}

	in := make([]float32, inRate) // one second of input
	out := make([]float32, block*3+64)
	total := 0
	for off := 0; off < len(in); off += block {
		d := &resample.Data{
			In:           in[off : off+block],
			InputFrames:  block,
			Out:          out,
			OutputFrames: len(out),
			Ratio:        ratio,
			EndOfInput:   off+block == len(in),
		}
		if err := r.Process(d); err != nil {
			panic(err)
		}
		total += d.OutputFramesGen // out[:d.OutputFramesGen] is ready to use
	}
	fmt.Println("output frames:", total)
	// Output: output frames: 48000
}
