package resample

// zohKernel implements ZeroOrderHold: the output takes the value of the most
// recent input sample.
type zohKernel struct{}

func (zohKernel) support(float64) (int, int) { return 0, 0 }

func (zohKernel) output(buf []float32, channels, base int, _, _ float64, dst []float32) {
	copy(dst[:channels], buf[base*channels:])
}
