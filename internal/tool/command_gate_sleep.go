package tool

import (
	"regexp"
	"strconv"
	"strings"
)

// Inline-sleep abuse guard: the sleep tool is hard-capped (3 minutes) and
// steers long waits to start_command/cron, but LLMs learned to smuggle the
// same long waits into run_command scripts ("sleep 300 && ./thing.sh").
// These thresholds mirror the sleep tool's semantics: blocking waits over
// the cap are rejected outright; moderately long ones draw a warning.
// Short polling sleeps (e.g. `sleep 5` retry loops) stay untouched.
const (
	inlineSleepBlockSeconds = 180.0
	inlineSleepWarnSeconds  = 60.0
)

// Matches sh sleep with an explicit duration; GNU/BSD suffixes s/m/h/d.
// \b keeps "mysleep 5" from matching (Go's RE2 has no lookahead).
var inlineShSleepRe = regexp.MustCompile(
	`\bsleep\b[ \t]+([0-9]+(?:\.[0-9]+)?)([smhdSMHD]?)\b`)

// Matches PowerShell Start-Sleep with -Seconds/-s (or bare positional).
var inlinePsSleepRe = regexp.MustCompile(
	`\bStart-Sleep\b[ \t]+(?:-[sS]econds[ \t]+|-s[ \t]+)?([0-9]+(?:\.[0-9]+)?)([smhdSMHD]?)\b`)

// inlineSleepSeconds sums every parseable inline sleep duration in cmd
// (seconds). Callers pass a quote-blanked view so sleeps inside string
// literals and heredocs are not counted. Unparseable sleeps (variables,
// substitutions) contribute 0; the companion regexes only admit literals.
func inlineSleepSeconds(cmd string) float64 {
	total := 0.0
	for _, re := range []*regexp.Regexp{inlineShSleepRe, inlinePsSleepRe} {
		for _, m := range re.FindAllStringSubmatch(cmd, -1) {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			switch strings.ToLower(m[2]) {
			case "m":
				v *= 60
			case "h":
				v *= 3600
			case "d":
				v *= 86400
			}
			total += v
		}
	}
	return total
}
