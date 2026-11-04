# go-resample

[![CI](https://github.com/gojargo/go-resample/actions/workflows/ci.yml/badge.svg)](https://github.com/gojargo/go-resample/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gojargo/go-resample.svg)](https://pkg.go.dev/github.com/gojargo/go-resample)
[![codecov](https://codecov.io/gh/gojargo/go-resample/graph/badge.svg)](https://codecov.io/gh/gojargo/go-resample)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/gojargo/go-resample/badge)](https://scorecard.dev/viewer/?uri=github.com/gojargo/go-resample)

A pure-Go (no cgo) audio sample-rate converter.

- **No cgo**: cross-compiles and links cleanly into static Go binaries.
- **Arbitrary & time-varying ratios** in `[1/256, 256]` (`ratio = output_rate / input_rate`).
- **Interleaved `float32`** frames, any channel count.

## Converters

| Converter | Quality | Cost | Notes |
|---|---|---|---|
| `SincBestQuality` | highest | high | widest passband, deepest stopband |
| `SincMediumQuality` | transparent for most uses | medium | good default |
| `SincFastest` | good | low | band-limited sinc, low CPU |
| `ZeroOrderHold` | low | trivial | nearest-sample hold |
| `Linear` | low | trivial | linear interpolation |

The sinc converters build a Kaiser-windowed sinc prototype filter and evaluate it
via a polyphase table with linear interpolation between phases; downsampling
widens the filter for anti-aliasing and rescales for unity gain.

## Usage

One-shot conversion of a whole buffer:

```go
out, err := resample.Simple(in, 48000.0/44100.0, 2, resample.SincMediumQuality)
```

Streaming:

```go
r, _ := resample.New(resample.SincMediumQuality, 2)
d := &resample.Data{
    In: in, InputFrames: len(in) / 2,
    Out: out, OutputFrames: len(out) / 2,
    Ratio: 2.0,
    EndOfInput: false, // set true on the final call
}
_ = r.Process(d)
// d.OutputFramesGen frames written; call again to feed more / drain.
```

Streaming notes:

- `Process` always consumes all of `InputFrames`. If `Out` was too small for
  everything that input can produce, call again with `InputFrames: 0` to drain.
- `Process` performs no heap allocations once warmed up, so it is safe to call
  from a real-time audio callback.
- Changing `Ratio` between calls ramps it smoothly over `OutputFrames`; use
  `SetRatio` for an immediate step change.
- A `Resampler` is not safe for concurrent use; use one per stream. Separate
  `Resampler`s can run in parallel freely (filter tables are shared read-only).

Requires Go 1.22 or later.

## Quality

Measured by the FFT harness in [`quality_test.go`](./quality_test.go)
(`go test -run Report -v`), worst case across the passband; CI enforces
conservative floors below these values.

| Converter | SNR (2× up) | Stopband rejection (2× down) | -3 dB bandwidth |
|---|---|---|---|
| `SincBestQuality` | ~158 dB | ~157 dB | 95% |
| `SincMediumQuality` | ~123 dB | ~92 dB | 87% |
| `SincFastest` | ~97 dB | ~85 dB | 78% |

Bandwidth is relative to the output Nyquist frequency when downsampling. To
downsample to a speech rate (for example 48 kHz to 16 kHz) without dulling
the top of the band, prefer `SincBestQuality`: its passband reaches 95% of the
new Nyquist frequency, against 87% for `SincMediumQuality`.

The [`test/`](./test) directory holds a separate differential harness that
compares the output against libsoxr (requires cgo and libsoxr).

## Performance

Single core, stereo, 20 ms blocks (`go test -bench Process`), on a recent
x86-64 desktop; expect roughly these orders of magnitude:

| Converter | 44.1 → 48 kHz | 16 → 48 kHz | 48 → 16 kHz |
|---|---|---|---|
| `SincBestQuality` | ~180× real time | ~200× | ~165× |
| `SincMediumQuality` | ~390× | ~390× | ~380× |
| `SincFastest` | ~570× | ~570× | ~770× |
| `Linear` | ~1500× | ~1300× | ~3600× |

When the ratio is a fraction with a small denominator, as for every standard
pair of audio rates, the sinc converters precompute the filter coefficients
for each position the stream can reach and track the read position exactly
(the "direct" mode of the Speex resampler). Arbitrary or continuously varying
ratios use the general interpolating path. The precomputed tables are built
on a stream's first `Process` call and shared by all streams in the process.

## Testing

```sh
go test ./...                        # full suite, including quality thresholds
go test -race -short ./...           # skips the slow FFT measurements
go test -run Report -v               # print the measured quality table
go test -run '^$' -bench .           # benchmarks
go test -run '^$' -fuzz FuzzProcess  # fuzz the streaming API
```

## License

BSD-2-Clause. See [`LICENSE`](./LICENSE). Credit for the reference
implementation this work is based on is in [`NOTICE`](./NOTICE).
