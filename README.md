# go-resample

A pure-Go (no cgo) audio sample-rate converter.

- **No cgo** — cross-compiles and links cleanly into static Go binaries.
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

## License

BSD-2-Clause — see [`LICENSE`](./LICENSE). Credit for the reference
implementation this work is based on is in [`NOTICE`](./NOTICE).
