package main

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedTranslationsAreValidJSON(t *testing.T) {
	t.Parallel()

	requiredKeys := []string{
		"menu.file",
		"im.title",
		"sidebar.tab.provider",
		"share.title",
		"update.title",
	}

	for _, lang := range []string{"en", "zh-CN"} {
		data, err := translationsFS.ReadFile("translations/" + lang + ".json")
		if err != nil {
			t.Fatalf("read %s translations: %v", lang, err)
		}

		var entries map[string]string
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatalf("parse %s translations: %v", lang, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s translations unexpectedly empty", lang)
		}
		for _, key := range requiredKeys {
			if entries[key] == "" {
				t.Fatalf("%s missing required translation key %q", lang, key)
			}
		}
	}
}
