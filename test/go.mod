// Separate (nested) module so the root go-resample module stays pure-Go and
// cgo-free: `go build ./...` / `go test ./...` at the repo root do not descend
// into this directory. This tool requires libsoxr (pkg-config: soxr).
module github.com/gojargo/go-resample/test

go 1.26

require github.com/gojargo/go-resample v0.1.0

replace github.com/gojargo/go-resample => ../
