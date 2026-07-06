package resample

import "errors"

var (
	// ErrBadConverter is returned when an unknown Converter value is used.
	ErrBadConverter = errors.New("resample: unknown converter type")
	// ErrBadChannelCount is returned when channels < 1.
	ErrBadChannelCount = errors.New("resample: channel count must be >= 1")
	// ErrBadRatio is returned when the conversion ratio is outside [1/256, 256].
	ErrBadRatio = errors.New("resample: conversion ratio out of range [1/256, 256]")
	// ErrBadData is returned when a Data argument is nil or internally inconsistent
	// (nil buffers, or frame counts that exceed the backing slice lengths).
	ErrBadData = errors.New("resample: invalid Data (nil or inconsistent buffers)")
)
