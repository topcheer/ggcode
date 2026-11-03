package agent

const (
	detectorTierCritical = 0
	detectorTierRoutine  = 2
)

func shouldRunDetector(tier, iteration int) bool {
	switch tier {
	case detectorTierCritical:
		return true
	case detectorTierRoutine:
		return iteration%3 == 1
	default:
		return true
	}
}
