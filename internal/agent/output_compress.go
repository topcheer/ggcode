package agent

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	exactDupThreshold = 3

	prefixSimilarThreshold = 5

	minPrefixLen = 10

	maxCompressInputLines = 5000
)

func compressRepetitiveLines(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < exactDupThreshold {
		return content
	}
	if len(lines) > maxCompressInputLines {
		return content
	}

	var out []string
	i := 0
	for i < len(lines) {

		runEnd := i + 1
		for runEnd < len(lines) && lines[runEnd] == lines[i] {
			runEnd++
		}
		runLen := runEnd - i
		if runLen >= exactDupThreshold {
			out = append(out, lines[i])
			out = append(out, formatDupMarker(runLen-1, lines[i]))
			i = runEnd
			continue
		}

		if len(lines[i]) >= minPrefixLen {
			prefix := lines[i][:minPrefixLen]
			sRunEnd := i + 1
			for sRunEnd < len(lines) && strings.HasPrefix(lines[sRunEnd], prefix) {
				sRunEnd++
			}
			sRunLen := sRunEnd - i
			if sRunLen >= prefixSimilarThreshold && foldLinesHomogeneous(lines[i:sRunEnd]) {

				out = append(out, lines[i])
				if i+1 < sRunEnd {
					out = append(out, lines[i+1])
				}
				if sRunEnd-1 > i+1 {
					out = append(out, lines[sRunEnd-1])
				}
				omitted := sRunLen - 3
				if omitted > 0 {
					out = append(out, formatSimilarMarker(omitted))
				}
				i = sRunEnd
				continue
			}
		}

		out = append(out, lines[i])
		i++
	}

	if len(out) == len(lines) {
		return content
	}
	return strings.Join(out, "\n")
}

func formatDupMarker(omitted int, sample string) string {
	display := sample
	if len(display) > 60 {
		display = display[:57] + "..."
	}
	if strings.TrimSpace(display) == "" {
		return "[" + strconv.Itoa(omitted) + " identical blank lines omitted]"
	}
	return "[" + strconv.Itoa(omitted) + " identical lines omitted: " + display + "]"
}

func formatSimilarMarker(omitted int) string {
	return "[" + strconv.Itoa(omitted) + " similar lines omitted]"
}

func foldLinesHomogeneous(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	for _, ln := range lines {
		if lineCarriesUniqueDigits(ln) {
			return false
		}
	}
	base := stripDigits(lines[0])
	if base == "" {
		return false
	}
	for _, ln := range lines[1:] {
		if stripDigits(ln) != base {
			return false
		}
	}
	return true
}

var reUniqueDigitShape = regexp.MustCompile(
	`\d{4}-\d{2}-\d{2}|:\d+|v?\d+\.\d+(\.\d+)?|_\d{3,}`)

func lineCarriesUniqueDigits(line string) bool {
	return reUniqueDigitShape.MatchString(line)
}

func stripDigits(s string) string {
	var b strings.Builder
	inDigit := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			inDigit = true
			continue
		}
		if inDigit {
			b.WriteByte('*')
			inDigit = false
		}
		b.WriteRune(r)
	}
	if inDigit {
		b.WriteByte('*')
	}
	return b.String()
}
