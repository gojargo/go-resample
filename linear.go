package resample

// linearKernel implements Linear: straight-line interpolation between the two
// nearest input samples.
type linearKernel struct{}

func (linearKernel) support(float64) (int, int) { return 0, 1 }

func (linearKernel) maxHalfTaps() int { return 1 }

func (linearKernel) output(buf []float32, channels, base, c int, frac, _ float64) float64 {
	i := base*channels + c
	a := float64(buf[i])
	b := a
	if j := i + channels; j < len(buf) {
		b = float64(buf[j])
	}
	return a + frac*(b-a)
}

func (linearKernel) reset() {}
