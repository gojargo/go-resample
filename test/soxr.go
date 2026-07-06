package main

/*
#cgo pkg-config: soxr
#include <soxr.h>

// Thin wrappers mirroring the reference (jargo) settings so the constant-taking
// spec constructors are resolved by the C compiler: libsoxr High Quality,
// interleaved int16 in/out, single-threaded runtime.
static soxr_io_spec_t      abtest_io_int16(void)   { return soxr_io_spec(SOXR_INT16_I, SOXR_INT16_I); }
static soxr_quality_spec_t abtest_quality_hq(void) { return soxr_quality_spec(SOXR_HQ, 0); }
static soxr_runtime_spec_t abtest_runtime_1t(void) { return soxr_runtime_spec(1); }
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// soxrResampler is a stateful streaming resampler backed by libsoxr, configured
// identically to the reference implementation (HQ, int16 interleaved, 1 thread).
type soxrResampler struct {
	inRate, outRate, channels int
	soxr                      C.soxr_t
}

func newSoxr(inRate, outRate, channels int) (*soxrResampler, error) {
	r := &soxrResampler{inRate: inRate, outRate: outRate, channels: channels}
	io := C.abtest_io_int16()
	q := C.abtest_quality_hq()
	rt := C.abtest_runtime_1t()
	var serr C.soxr_error_t
	r.soxr = C.soxr_create(
		C.double(inRate), C.double(outRate), C.uint(channels),
		&serr, &io, &q, &rt) //nolint:gocritic
	if serr != nil {
		return nil, fmt.Errorf("soxr_create %d->%d ch=%d: %s",
			inRate, outRate, channels, C.GoString((*C.char)(unsafe.Pointer(serr))))
	}
	return r, nil
}

// process resamples one chunk of interleaved int16 frames, carrying filter state
// across calls (streaming).
func (r *soxrResampler) process(in []int16) []int16 {
	ch := r.channels
	inFrames := len(in) / ch
	if inFrames == 0 {
		return nil
	}
	outFrames := inFrames*r.outRate/r.inRate + 64
	out := make([]int16, outFrames*ch)

	var idone, odone C.size_t
	serr := C.soxr_process(r.soxr,
		C.soxr_in_t(unsafe.Pointer(&in[0])), C.size_t(inFrames), &idone,
		C.soxr_out_t(unsafe.Pointer(&out[0])), C.size_t(outFrames), &odone)
	if serr != nil {
		return nil
	}
	return out[:int(odone)*ch]
}

func (r *soxrResampler) close() {
	if r.soxr != nil {
		C.soxr_delete(r.soxr)
		r.soxr = nil
	}
}
