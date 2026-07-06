package resample

// zohKernel implements ZeroOrderHold: the output takes the value of the most
// recent input sample.
type zohKernel struct{}

func (zohKernel) support(float64) (int, int) { return 0, 0 }

func (zohKernel) maxHalfTaps() int { return 1 }

func (zohKernel) output(buf []float32, channels, base, c int, _, _ float64) float64 {
	return float64(buf[base*channels+c])
}

func (zohKernel) reset() {}
