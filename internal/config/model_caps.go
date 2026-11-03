package config

func ModelSupportsVision(model string) bool {
	return inferVisionSupport(model, "")
}

func ModelContextWindow(model string) int {
	return inferContextWindow(model, "")
}

func annotateVisionFlags(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if ModelSupportsVision(m) {
			out = append(out, m+" [vision]")
		} else {
			out = append(out, m)
		}
	}
	return out
}
