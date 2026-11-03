package memory

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

var preferenceMarkers = []string{

	"from now on",
	"from here on",
	"going forward",
	"always use",
	"always run",
	"always prefer",
	"never use",
	"never run",
	"don't use",
	"do not use",
	"remember to",
	"remember that",

	"以后都",
	"以后请",
	"以后用",
	"以后改用",
	"以后不要",
	"以后不再",
	"以后一律",
	"从现在起",
	"从今以后",
	"别再",
	"记住要",
	"记住：",
	"记住:",
}

var correctiveMarkers = []string{

	"no, use",
	"no, run",
	"instead of",
	"instead, ",

	"不对，",
	"不对,",
	"别用",
	"换成",
	"改用",
}

var correctiveActionWords = []string{
	"use", "run", "go with", "prefer", "switch", "install",
}

var selfSufficientCorrectiveMarkers = []string{
	"别用", "改用", "换成",
}

var correctiveActionWordsZH = []string{
	"使用", "运行", "执行", "安装", "换掉",
}

var correctiveZHVerbObjectChars = []rune{'用', '换', '跑', '装'}

const (
	maxPreferencesPerRun = 2

	maxPreferenceLineLen = 240

	maxPreferenceEntries = 30
)

func DistillUserPreferences(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var prefs []string
	seen := map[string]bool{}
	for _, sent := range splitPreferenceSentences(text) {
		sent = strings.TrimSpace(sent)
		if sent == "" || !isPreferenceSentence(sent) {
			continue
		}
		if utf8.RuneCountInString(sent) > maxPreferenceLineLen {

			runes := []rune(sent)
			sent = strings.TrimSpace(string(runes[:maxPreferenceLineLen]))
		}
		if seen[sent] {
			continue
		}
		seen[sent] = true
		prefs = append(prefs, sent)
		if len(prefs) >= maxPreferencesPerRun {
			break
		}
	}
	return prefs
}

func isPreferenceSentence(sent string) bool {
	lower := strings.ToLower(sent)
	for _, m := range preferenceMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}

	for _, c := range correctiveMarkers {
		if !strings.Contains(lower, strings.ToLower(c)) {
			continue
		}
		for _, s := range selfSufficientCorrectiveMarkers {
			if strings.Contains(lower, s) {
				return true
			}
		}
		for _, a := range correctiveActionWordsZH {
			if strings.Contains(lower, a) {
				return true
			}
		}
		if containsZHVerbObject(lower) {
			return true
		}
		if containsWordBoundary(lower, correctiveActionWords) {
			return true
		}
		return false
	}
	return false
}

func containsZHVerbObject(s string) bool {
	for _, c := range correctiveZHVerbObjectChars {
		start := 0
		for {
			i := strings.IndexRune(s[start:], c)
			if i < 0 {
				break
			}
			pos := start + i
			tail := s[pos+utf8.RuneLen(c):]
			if tail != "" {
				r, _ := utf8.DecodeRuneInString(tail)
				if r == ' ' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
					return true
				}
			}
			start = pos + utf8.RuneLen(c)
			if start >= len(s) {
				break
			}
		}
	}
	return false
}

func containsWordBoundary(s string, words []string) bool {
	for _, w := range words {
		start := 0
		for {
			i := strings.Index(s[start:], w)
			if i < 0 {
				break
			}
			pos := start + i
			end := pos + len(w)
			beforeOK := pos == 0 || !unicode.IsLetter(rune(s[pos-1]))
			afterOK := end == len(s) || !unicode.IsLetter(rune(s[end]))
			if beforeOK && afterOK {
				return true
			}
			start = pos + 1
			if start >= len(s) {
				break
			}
		}
	}
	return false
}

func CapturePreferences(workingDir, text string) int {
	prefs := DistillUserPreferences(text)
	if len(prefs) == 0 {
		return 0
	}
	if workingDir == "" {
		return 0
	}
	autoMem := NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return 0
	}
	const key = "user-preferences"

	existing, err := autoMem.LoadKey(key)
	if err != nil {
		debug.Log("preferences", "failed to load existing, skipping save: %v", err)
		return 0
	}
	merged, added := MergePreferenceMemory(existing, prefs)
	if added == 0 {
		return 0
	}
	if err := autoMem.SaveMemory(key, merged); err != nil {
		debug.Log("preferences", "save failed: %v", err)
		return 0
	}
	debug.Log("preferences", "captured %d new user preference(s)", added)
	return added
}

func splitPreferenceSentences(text string) []string {
	var parts []string
	var cur []rune
	runes := []rune(text)
	for i, r := range runes {
		switch r {
		case '\n', '\r', '。', '！', '？':
			parts = append(parts, string(cur))
			cur = cur[:0]
			continue
		case '.', '!', '?':
			if i+1 < len(runes) && isPreferenceWordRune(runes[i+1]) {
				cur = append(cur, r)
				continue
			}
			parts = append(parts, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, r)
	}
	parts = append(parts, string(cur))
	return parts
}

func isPreferenceWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r >= 0x4e00 && r <= 0x9fff:
		return true
	}
	return false
}

func MergePreferenceMemory(existing string, prefs []string) (string, int) {
	var lines []string
	for _, l := range strings.Split(existing, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		lines = append(lines, strings.TrimPrefix(l, "- "))
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[strings.ToLower(l)] = true
	}
	added := 0
	for _, p := range prefs {
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, p)
		added++
	}
	if len(lines) > maxPreferenceEntries {
		lines = lines[len(lines)-maxPreferenceEntries:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), added
}
