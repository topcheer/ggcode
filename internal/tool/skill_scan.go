package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type scanSkillPattern struct {
	label   string
	pattern *regexp.Regexp
}

var skillInjectionPatterns = []scanSkillPattern{
	{
		label:   "instruction-override attempt",
		pattern: regexp.MustCompile(`(?i)\b(ignore|disregard|forget)\b[^.\n]{0,40}\b(all\s+)?(previous|prior|above|earlier|standing)\b[^.\n]{0,40}\b(instruction|prompt|rule|directive)s?\b`),
	},
	{
		label:   "system-prompt exfiltration attempt",
		pattern: regexp.MustCompile(`(?i)\b(reveal|print|show|output|exfiltrate|repeat|include|leak)\b[^.\n]{0,60}\b(system|developer)\s+(prompt|message|instruction)s?\b`),
	},
}

var skillCommandPatterns = []scanSkillPattern{
	{
		label:   "remote pipe-to-shell",
		pattern: regexp.MustCompile("(?i)\\b(curl|wget)\\b[^\\n|]{0,120}\\|\\s*(sudo\\s+)?(ba|z|da|k)?sh\\b"),
	},
	{
		label:   "recursive root deletion",
		pattern: regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*[rR][a-zA-Z]*[fF][a-zA-Z]*\s+|\s*-[a-zA-Z]*[fF][a-zA-Z]*[rR][a-zA-Z]*\s+)+/(\s|$|;|&|\||-|home\b|etc\b|usr\b|var\b|root\b|bin\b|boot\b)`),
	},
	{
		label:   "credential exfiltration via network",
		pattern: regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n]{0,160}\b(api[_-]?key|authorization|bearer|secret|passw(or)?d|\.env|credentials?|~/.ssh|id_rsa)\b`),
	},
}

var suspiciousSkillRunes = map[rune]string{
	'\u200b': "zero-width space", '\u200c': "zero-width non-joiner",
	'\u200d': "zero-width joiner", '\u200e': "left-to-right mark",
	'\u200f': "right-to-left mark", '\u202a': "left-to-right embedding",
	'\u202b': "right-to-left embedding", '\u202c': "pop directional formatting",
	'\u202d': "left-to-right override", '\u202e': "right-to-left override",
	'\u2060': "word joiner", '\u2061': "function application",
	'\u2062': "invisible times", '\u2063': "invisible separator",
	'\u2064': "invisible plus", '\ufeff': "zero-width no-break space",
	'\u3164': "hangul filler",
}

const scanSkillMaxFileBytes = 256 * 1024

func scanSkillText(relName string, content []byte) []string {
	var warns []string
	if idx := strings.IndexByte(string(content), 0); idx >= 0 {
		return nil
	}
	text := string(content)
	for _, grp := range [][]scanSkillPattern{skillInjectionPatterns, skillCommandPatterns} {
		for _, p := range grp {
			if loc := p.pattern.FindStringIndex(text); loc != nil {
				line := 1 + strings.Count(text[:loc[0]], "\n")
				excerpt := text[loc[0]:loc[1]]
				if len(excerpt) > 60 {
					excerpt = excerpt[:60] + "..."
				}
				warns = append(warns, fmt.Sprintf("%s:%d %s: %q", relName, line, p.label, excerpt))
			}
		}
	}
	seen := map[rune]bool{}
	for _, r := range text {
		if name, ok := suspiciousSkillRunes[r]; ok && !seen[r] {
			seen[r] = true
			warns = append(warns, fmt.Sprintf("%s hidden unicode (%s, U+%04X)", relName, name, r))
		}
	}
	return warns
}

func scanSkillDir(dir string) []string {
	var warns []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.Size() > scanSkillMaxFileBytes {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			rel = filepath.Base(p)
		}
		warns = append(warns, scanSkillText(filepath.ToSlash(rel), data)...)
		return nil
	})
	return warns
}
