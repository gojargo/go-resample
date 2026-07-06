// Command go-resample-abtest is a differential A/B harness comparing go-resample
// against libsoxr (configured exactly like the reference: HQ, int16, 1 thread)
// across representative voice sample-rate conversions.
//
// For each flow it streams the same int16 signal (in realistic 20 ms chunks)
// through both resamplers, then aligns the outputs (integer + fractional delay)
// and reports:
//
//   - null (dB):     delay-aligned residual, how close the two outputs are.
//   - magdiff (dB):  worst passband magnitude difference (delay-invariant).
//   - worstSeg (dB): worst localized-window residual, a glitch detector.
//
// Requires libsoxr at build/run time (pkg-config: soxr). int16 quantization
// bounds the achievable null at roughly -90 dB, so numbers near that mean the
// two are identical modulo 16-bit rounding.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"

	resample "github.com/gojargo/go-resample"
)

const durationSec = 4.0

func main() {
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		runDebug()
		return
	}
	flows := []struct{ in, out int }{
		{24000, 48000}, // TTS 24k -> WebRTC/Opus 48k
		{16000, 48000}, // STT-rate 16k -> 48k
		{8000, 48000},  // telephony 8k -> 48k
		{48000, 16000}, // mic 48k -> STT 16k
		{48000, 24000}, // 48k -> 24k
		{44100, 48000}, // 44.1k -> 48k (non-integer)
	}

	fmt.Printf("go-resample vs libsoxr(HQ,int16)  —  %.0fs signal, 20ms streaming chunks, mono\n", durationSec)
	fmt.Printf("int16 quantization floor ~ -90 dB (a null near that = identical modulo 16-bit rounding)\n\n")
	fmt.Printf("%-16s %-11s %10s %11s %11s %9s   %s\n",
		"flow", "go-tier", "voiceNull", "fullNull", "magdiff", "delay", "verdict")
	fmt.Printf("%s\n", dashes(100))

	for _, f := range flows {
		sig := genSignal(f.in, durationSec)
		for _, tier := range []struct {
			name string
			conv resample.Converter
		}{
			{"SincBest", resample.SincBestQuality},
			{"SincMedium", resample.SincMediumQuality},
		} {
			soxOut := runSoxr(f.in, f.out, sig)
			goOut := runGo(f.in, f.out, sig, tier.conv)
			m := analyze(toF64(soxOut), toF64(goOut), f.in, f.out)
			fmt.Printf("%-16s %-11s %8.1fdB %9.1fdB %9.2fdB %8dsp   %s\n",
				fmt.Sprintf("%d->%d", f.in, f.out), tier.name,
				m.voiceNullDB, m.fullNullDB, m.magDiffDB, m.lag, verdict(m))
		}
	}
	fmt.Printf("\nvoiceNull (<=7kHz): the speech-relevant residual. fullNull: whole band, incl. the\n")
	fmt.Printf("transition/stopband near Nyquist where the two filters legitimately differ.\n")
	fmt.Printf("More negative = closer; magdiff smaller = closer.\n")
}

// runSoxr streams the signal through libsoxr in 20 ms chunks.
func runSoxr(inRate, outRate int, sig []int16) []int16 {
	r, err := newSoxr(inRate, outRate, 1)
	if err != nil {
		panic(err)
	}
	defer r.close()
	chunk := inRate / 50 // 20 ms
	var out []int16
	for off := 0; off < len(sig); off += chunk {
		out = append(out, r.process(sig[off:min(off+chunk, len(sig))])...)
	}
	return out
}

// runGo streams the signal through go-resample in 20 ms chunks, applying the
// int16<->float32 conversion a jargo integration would need.
func runGo(inRate, outRate int, sig []int16, conv resample.Converter) []int16 {
	r, err := resample.New(conv, 1)
	if err != nil {
		panic(err)
	}
	ratio := float64(outRate) / float64(inRate)
	chunk := inRate / 50
	fbuf := make([]float32, chunk)
	outCap := int(float64(chunk)*ratio) + 4096
	fout := make([]float32, outCap)
	var out []int16
	for off := 0; off < len(sig); off += chunk {
		end := min(off+chunk, len(sig))
		n := end - off
		for i := 0; i < n; i++ {
			fbuf[i] = float32(sig[off+i]) / 32768.0
		}
		d := &resample.Data{
			In: fbuf[:n], Out: fout,
			InputFrames: n, OutputFrames: outCap,
			Ratio: ratio, EndOfInput: false,
		}
		_ = r.Process(d)
		for i := 0; i < d.OutputFramesGen; i++ {
			out = append(out, f32ToI16(fout[i]))
		}
	}
	return out
}

func f32ToI16(v float32) int16 {
	x := float64(v) * 32768.0
	x = math.Round(x)
	if x > 32767 {
		x = 32767
	}
	if x < -32768 {
		x = -32768
	}
	return int16(x)
}

func toF64(x []int16) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v) / 32768.0
	}
	return out
}

// genSignal builds a deterministic, broadband, voice-like int16 test signal:
// a low-level log chirp (up to 0.45*rate to stress anti-aliasing on downsampling),
// alternating voiced harmonic stacks (with onset/offset envelopes = boundary
// transients) and unvoiced noise bursts.
func genSignal(rate int, seconds float64) []int16 {
	n := int(float64(rate) * seconds)
	x := make([]float64, n)
	rng := rand.New(rand.NewSource(0x5eed))
	fr := float64(rate)

	f0, f1 := 100.0, 0.45*fr
	for i := 0; i < n; i++ {
		t := float64(i) / fr
		x[i] += 0.15 * math.Sin(2*math.Pi*(f0*t+(f1-f0)*t*t/(2*seconds)))
	}

	for seg := 0.0; seg < seconds; seg += 0.5 {
		start := int(seg * fr)
		if int(math.Round(seg*10))%10 < 7 { // voiced ~0.35s
			f := 130.0 * (1 + 0.01*math.Sin(2*math.Pi*5*seg))
			end := min(int((seg+0.35)*fr), n)
			for i := start; i < end; i++ {
				tt := float64(i-start) / fr
				env := math.Sin(math.Pi * tt / 0.35)
				var s float64
				for h := 1; h <= 25; h++ {
					if f*float64(h) > 0.45*fr {
						break
					}
					s += (1.0 / float64(h)) * math.Sin(2*math.Pi*f*float64(h)*tt)
				}
				x[i] += 0.5 * env * s / 3.0
			}
		} else { // unvoiced burst ~0.15s
			end := min(int((seg+0.15)*fr), n)
			for i := start; i < end; i++ {
				x[i] += 0.25 * (rng.Float64()*2 - 1)
			}
		}
	}

	peak := 1e-9
	for _, v := range x {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	sc := 0.8 / peak * 32767
	out := make([]int16, n)
	for i, v := range x {
		q := math.Round(v * sc)
		if q > 32767 {
			q = 32767
		}
		if q < -32768 {
			q = -32768
		}
		out[i] = int16(q)
	}
	return out
}

func verdict(m metrics) string {
	switch {
	case m.voiceNullDB < -60 && m.magDiffDB < 0.3:
		return "EXCELLENT (indistinguishable for voice)"
	case m.voiceNullDB < -45 && m.magDiffDB < 1.0:
		return "GOOD (equivalent for voice)"
	case m.voiceNullDB < -30:
		return "OK (minor voice-band differences)"
	default:
		return "CHECK"
	}
}

func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
