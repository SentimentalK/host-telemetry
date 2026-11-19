package rate

import "time"

func Rate(prev, cur float64, prevMono, curMono time.Time) (float64, bool) {
	dt := curMono.Sub(prevMono).Seconds()
	if dt <= 0 || cur < prev {
		return 0, false
	}
	return (cur - prev) / dt, true
}

func Utilization(prev, cur float64, prevMono, curMono time.Time, units float64) (float64, bool) {
	if units <= 0 {
		units = 1
	}
	value, ok := Rate(prev, cur, prevMono, curMono)
	if !ok {
		return 0, false
	}
	value /= units
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	return value, true
}
