package resample

import (
	"math"
	"testing"
)

// FuzzProcess drives arbitrary input, converter, channel count, ratio, chunk
// size and output-buffer size through the streaming API and checks that:
//
//   - Process never fails or panics on valid arguments,
//   - frame accounting is consistent,
//   - output is finite and bounded for bounded input,
//   - streaming matches a one-shot Simple conversion of the same input,
//   - the precomputed-phase path matches the general interpolating path.
//
// `go test` runs the seed corpus; `go test -fuzz=FuzzProcess` explores further.
func FuzzProcess(f *testing.F) {
	f.Add([]byte("the quick brown fox jumps over the lazy dog"), uint8(0), uint8(0), uint16(600), uint16(7), uint16(3))
	f.Add([]byte{0, 255, 0, 255, 128, 127, 1, 254}, uint8(1), uint8(1), uint16(100), uint16(1), uint16(1))
	f.Add(make([]byte, 300), uint8(2), uint8(2), uint16(1023), uint16(50), uint16(1000))
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 0, 0}, uint8(3), uint8(3), uint16(0), uint16(2), uint16(2))
	f.Add([]byte("0123456789abcdef0123456789abcdef"), uint8(4), uint8(0), uint16(512), uint16(5), uint16(4))

	f.Fuzz(func(t *testing.T, data []byte, convSel, chSel uint8, ratioSel, chunkSel, outSel uint16) {
		conv := Converter(int(convSel) % 5)
		ch := 1 + int(chSel)%4
		// Half the inputs use a standard rate pair (which takes the
		// precomputed-phase path), half a log-uniform ratio in [1/16, 16).
		standard := []float64{48000.0 / 44100.0, 44100.0 / 48000.0, 3, 1.0 / 3, 6, 1.0 / 6, 2, 0.5, 44100.0 / 16000.0, 16000.0 / 44100.0}
		ratio := math.Exp2(float64(ratioSel%1024)/1024*8 - 4)
		if ratioSel&1024 != 0 {
			ratio = standard[int(ratioSel)%len(standard)]
		}
		chunk := 1 + int(chunkSel)%512
		outCap := 1 + int(outSel)%1024

		frames := min(len(data)/ch, 2048)
		in := make([]float32, frames*ch)
		for i := range in {
			in[i] = float32(int8(data[i])) / 128 // [-1, 1)
		}

		want, err := Simple(in, ratio, ch, conv)
		if err != nil {
			t.Fatalf("Simple: %v", err)
		}
		r, err := New(conv, ch)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		got := streamAll(t, r, in, ratio, chunk, outCap)

		// Sinc filters ring, so allow overshoot above the input peak of 1.
		bound := 1.0
		if conv <= SincFastest {
			bound = 3.0
		}
		for i, v := range got {
			if math.IsNaN(float64(v)) || math.Abs(float64(v)) > bound {
				t.Fatalf("sample %d = %g out of bounds (%g)", i, v, bound)
			}
		}
		if d := len(got) - len(want); d < -ch || d > ch {
			t.Fatalf("streamed %d frames, Simple %d", len(got)/ch, len(want)/ch)
		}
		for i := 0; i < min(len(got), len(want)); i++ {
			if e := math.Abs(float64(got[i] - want[i])); e > 1e-4 {
				t.Fatalf("sample %d: streamed %g vs Simple %g", i, got[i], want[i])
			}
		}

		// The precomputed-phase path must match the general path.
		var general []float32
		withPhaseTables(false, func() { general, err = Simple(in, ratio, ch, conv) })
		if err != nil || len(general) != len(want) {
			t.Fatalf("general path: %d frames (err %v), precomputed %d", len(general)/ch, err, len(want)/ch)
		}
		for i := range general {
			if e := math.Abs(float64(general[i] - want[i])); e > 1e-4 {
				t.Fatalf("sample %d: general path %g vs precomputed %g", i, general[i], want[i])
			}
		}
	})
}
