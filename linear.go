package resample

// linearKernel implements Linear: straight-line interpolation between the two
// nearest input samples.
type linearKernel struct{}

func (linearKernel) support(float64) (int, int) { return 0, 1 }

func (linearKernel) output(buf []float32, channels, base int, frac, _ float64, dst []float32) {
	i := base * channels
	next := i + channels
	hasNext := next+channels <= len(buf)
	for c := range dst[:channels] {
		a := float64(buf[i+c])
		b := a
		if hasNext {
			b = float64(buf[next+c])
		}
		dst[c] = float32(a + frac*(b-a))
	}
}
