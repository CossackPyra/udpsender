package udpsender

import "math"

func movingExpAvg(value float64, oldValue float64, ftime float64, time float64) float64 {
	alpha := 1. - math.Exp(-ftime/time)
	r := alpha*value + (1.-alpha)*oldValue
	return r
}
