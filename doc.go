// Package resample is a pure-Go (no cgo) audio sample-rate converter.
// See the NOTICE and LICENSE files for attribution.
//
// The package supports arbitrary and time-varying conversion ratios in the
// range [1/256, 256], where the ratio is defined as output_rate / input_rate.
// Audio is processed as interleaved float32 frames (one sample per channel per
// frame).
//
// # Converters
//
// Five converters trade quality against CPU cost:
//
//   - SincBestQuality   – widest bandwidth, deepest stopband, highest cost
//   - SincMediumQuality – transparent for most uses, moderate cost
//   - SincFastest       – good quality, low cost
//   - ZeroOrderHold     – trivial, nearest-sample hold
//   - Linear            – trivial, linear interpolation
//
// # Usage
//
// For a one-shot conversion of a whole buffer, use [Simple]:
//
//	out, err := resample.Simple(in, 48000.0/44100.0, 2, resample.SincMediumQuality)
//
// For streaming, create a [Resampler] and call [Resampler.Process] repeatedly,
// setting Data.EndOfInput on the final call.
package resample
