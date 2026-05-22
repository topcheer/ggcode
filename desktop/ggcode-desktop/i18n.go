package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed translations
var translationsFS embed.FS

var (
	translations     = map[string]map[string]string{}
	translationsOnce sync.Once
	translationsMu   sync.RWMutex
	currentLang      = "en"
)

func normalizeLanguage(lang string) string {
	switch lang {
	case "", "en", "zh-CN":
		if lang == "" {
			return "en"
		}
		return lang
	default:
		return "en"
	}
}

func loadTranslations() {
	translationsOnce.Do(func() {
		for _, lang := range []string{"en", "zh-CN"} {
			data, err := translationsFS.ReadFile("translations/" + lang + ".json")
			if err != nil {
				continue
			}
			var m map[string]string
			if json.Unmarshal(data, &m) == nil {
				translationsMu.Lock()
				translations[lang] = m
				translationsMu.Unlock()
			}
		}
	})
}

// setLanguage updates the current language.
func setLanguage(lang string) {
	loadTranslations()
	translationsMu.Lock()
	currentLang = normalizeLanguage(lang)
	translationsMu.Unlock()
}

// t translates a key using the current language.
func t(key string, args ...any) string {
	loadTranslations()
	translationsMu.RLock()
	lang := currentLang
	m := translations[lang]
	translationsMu.RUnlock()
	if m == nil {
		translationsMu.RLock()
		m = translations["en"]
		translationsMu.RUnlock()
	}
	msg, ok := m[key]
	if !ok {
		translationsMu.RLock()
		fallback := translations["en"]
		translationsMu.RUnlock()
		if fallback != nil {
			msg, ok = fallback[key]
		}
	}
	if msg == "" {
		msg = key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}
