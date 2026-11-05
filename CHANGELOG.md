# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.1.0] - 2026-11-05

### Added

- Precomputed-phase evaluation for the sinc converters, after the "direct"
  mode of the Speex resampler. When the ratio is a fraction with a small
  denominator (every standard pair of audio rates), the filter coefficients
  for each reachable position are computed once and the read position is
  tracked exactly. Steady-state throughput is about 2.7x higher for mono and
  1.9x higher for stereo at the same quality. Tables are built on a stream's
  first `Process` call and shared by all streams; other ratios use the general
  path unchanged.
- Runnable examples for `Simple` and `Resampler.Process`.
- Benchmarks (`go test -bench .`) and a fuzz test (`FuzzProcess`).
- `SECURITY.md` with private vulnerability reporting.

### Changed

- The sinc kernels evaluate each filter tap once per output frame for all
  channels, with dedicated mono and stereo paths: about 45% faster for stereo
  on their own, with bit-identical output on amd64.
- Sinc filter tables are designed once per converter and shared read-only by
  all Resamplers, so `New` costs about 300 ns instead of up to 2.4 ms.
- `Simple` feeds its input to the resampler in chunks instead of copying the
  whole input into the internal history buffer, and grows its output if the
  size estimate is ever short.
- The minimum Go version is now 1.22 (was 1.26).
- The FFT quality tests are skipped with `-short`, and the quality report runs
  only with `-v`.

### Fixed

- Output length could be one frame too long (for example 48001 frames for one
  second of 16 kHz audio upsampled to 48 kHz) because the read position
  accumulated rounding error. The position is now snapped to the nearest input
  frame when within 1e-8 frames of it, and is exact on the precomputed-phase
  path. Streaming and one-shot conversion now agree exactly, including for
  `ZeroOrderHold`.
- The `Process` documentation now states that a ratio change is ramped over
  `OutputFrames` (the capacity of `Out`), not over the frames produced.

### Removed

- The compiled A/B harness binary that was committed under `test/`.

## [1.0.1] - 2026-07-13

No library changes. Adds a security workflow (Trivy and CodeQL) and README
badges.

## [1.0.0] - 2026-07-06

### Added

- `Process` performs no heap allocations once its internal buffer has
  reached its steady-state size, making it safe for real-time audio callbacks.
- A differential A/B harness comparing the output against libsoxr, as a
  separate module under `test/`.

## [0.1.0] - 2026-07-06

### Added

- Initial release: a pure-Go audio sample-rate converter with three
  Kaiser-windowed sinc converters (`SincBestQuality`, `SincMediumQuality`,
  `SincFastest`) plus `Linear` and `ZeroOrderHold`, arbitrary and
  time-varying ratios in [1/256, 256], interleaved `float32` frames with any
  channel count, and streaming (`Process`) and one-shot (`Simple`) APIs.

[Unreleased]: https://github.com/gojargo/go-resample/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/gojargo/go-resample/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/gojargo/go-resample/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/gojargo/go-resample/compare/v0.1.0...v1.0.0
[0.1.0]: https://github.com/gojargo/go-resample/releases/tag/v0.1.0
