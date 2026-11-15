package tui

import "testing"

// /et 命令族 i18n 钉测：en/zh 目录键完整且无中文泄漏到 en 面。
func TestEtCatalogCompleteness(t *testing.T) {
	en := enEtCatalog()
	zh := zhEtCatalog()
	if len(en) != len(zh) {
		t.Fatalf("et catalog key mismatch: en=%d zh=%d", len(en), len(zh))
	}
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Fatalf("zh missing et key: %s", k)
		}
	}
}

func TestEtCatalogEnglishKeysNoHan(t *testing.T) {
	for k, v := range enEtCatalog() {
		if containsHan(v) {
			t.Fatalf("en catalog key %s contains Han characters: %q", k, v)
		}
	}
}

func TestEtStatusInactiveRenders(t *testing.T) {
	en := tr(LangEnglish, "et.status_inactive", "free")
	zh := tr(LangZhCN, "et.status_inactive", "free")
	if en == "" || zh == "" {
		t.Fatal("et.status_inactive empty render")
	}
	// en face must be English regardless of hardcode regression
	if containsHan(en) {
		t.Fatalf("en status line contains Han: %q", en)
	}
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
