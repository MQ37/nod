package decider

import "math"

// roundN is Python 3 round: half goes to even.
func roundN(x float64, places int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	pow := math.Pow10(places)
	y := x * pow
	neg := y < 0
	abs := math.Abs(y)
	floor := math.Floor(abs)
	diff := abs - floor
	var rounded float64
	switch {
	case diff > 0.5:
		rounded = floor + 1
	case diff < 0.5:
		rounded = floor
	default:
		if int64(floor)%2 == 0 {
			rounded = floor
		} else {
			rounded = floor + 1
		}
	}
	if neg {
		rounded = -rounded
	}
	return rounded / pow
}
