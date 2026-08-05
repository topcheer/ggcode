package agent

import (
	"strings"
	"testing"
)

func TestCheckI18n_LocaleMethodNoArg(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "toLocaleDateString without locale",
			content: `const d = new Date().toLocaleDateString();`,
			want:    "toLocaleDateString",
		},
		{
			name:    "toLocaleTimeString without locale",
			content: `const t = new Date().toLocaleTimeString();`,
			want:    "toLocaleTimeString",
		},
		{
			name:    "toLocaleString without locale",
			content: `const s = num.toLocaleString();`,
			want:    "toLocaleString",
		},
		{
			name:    "toLocaleDateString with locale arg - no warning",
			content: `const d = new Date().toLocaleDateString('en-US');`,
			want:    "",
		},
		{
			name:    "toLocaleDateString with options but no locale - no warning",
			content: `const d = new Date().toLocaleDateString(undefined, {year: 'numeric'});`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkI18n("test.js", "", tt.content)
			if tt.want == "" {
				if len(warnings) > 0 {
					t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
				}
				return
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got %v", tt.want, warnings)
			}
		})
	}
}

func TestCheckI18n_IntlFormatNoArg(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "Intl.NumberFormat without locale",
			content: `const fmt = new Intl.NumberFormat();`,
			want:    "NumberFormat",
		},
		{
			name:    "Intl.DateTimeFormat without locale",
			content: `const fmt = new Intl.DateTimeFormat();`,
			want:    "DateTimeFormat",
		},
		{
			name:    "Intl.NumberFormat with locale - no warning",
			content: `const fmt = new Intl.NumberFormat('de-DE');`,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkI18n("test.js", "", tt.content)
			if tt.want == "" {
				if len(warnings) > 0 {
					t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
				}
				return
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got %v", tt.want, warnings)
			}
		})
	}
}

func TestCheckI18n_HardcodedDateFormat(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "moment.js style YYYY-MM-DD",
			content: `const fmt = moment().format("YYYY-MM-DD");`,
			want:    "YYYY-MM-DD",
		},
		{
			name:    "date-fns style MM/DD/YYYY",
			content: `const s = formatDate(d, "MM/DD/YYYY");`,
			want:    "MM/DD/YYYY",
		},
		{
			name:    "strftime style %Y-%m-%d",
			content: `const s = strftime("%Y-%m-%d", d);`,
			want:    "%Y-%m-%d",
		},
		{
			name:    "DD/MM/YYYY format",
			content: `const s = moment().format("DD/MM/YYYY");`,
			want:    "DD/MM/YYYY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkI18n("test.js", "", tt.content)
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got %v", tt.want, warnings)
			}
		})
	}
}

func TestCheckI18n_CurrencyInLiteral(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "euro symbol in string",
			content: "const price = \"\u20ac\" + amount;",
			want:    "currency",
		},
		{
			name:    "pound symbol in string",
			content: "const display = \"Total: \u00a3\" + total;",
			want:    "currency",
		},
		{
			name:    "dollar concatenation",
			content: `return "$" + amount.toFixed(2);`,
			want:    "currency",
		},
		{
			name:    "yen symbol in string",
			content: "const label = \"\u00a5\" + price;",
			want:    "currency",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkI18n("test.js", "", tt.content)
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tt.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected warning containing %q, got %v", tt.want, warnings)
			}
		})
	}
}

func TestCheckI18n_GoHardcodedTimeFormat(t *testing.T) {
	content := `package main
import "time"
func main() {
	s := time.Now().Format("2006-01-02")
	_ = s
}`
	warnings := checkI18n("test.go", "", content)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "time.Format") || strings.Contains(w, "i18n") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected i18n warning for hardcoded time format, got %v", warnings)
	}
}

func TestCheckI18n_NoFalsePositives(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "locale-aware date formatting",
			content: `const d = new Date().toLocaleDateString('en-US', {year: 'numeric', month: 'long'});`,
		},
		{
			name:    "Intl with locale",
			content: `const fmt = new Intl.NumberFormat('ja-JP', {style: 'currency', currency: 'JPY'});`,
		},
		{
			name:    "empty file",
			content: "",
		},
		{
			name:    "unrelated code",
			content: `const x = 1 + 2;\nconsole.log(x);`,
		},
		{
			name:    "non-JS non-Go file",
			content: `<html><body>Hello</body></html>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := checkI18n("test.py", "", tt.content)
			if len(warnings) > 0 {
				t.Errorf("expected no warnings for %q, got: %v", tt.name, warnings)
			}
		})
	}
}

func TestCheckI18n_WarningCap(t *testing.T) {
	// Generate content with many i18n issues to test cap
	content := `
const a = new Date().toLocaleDateString();
const b = new Date().toLocaleTimeString();
const c = new Date().toLocaleString();
const d = new Intl.NumberFormat();
const e = new Intl.DateTimeFormat();
const f = moment().format("YYYY-MM-DD");
const g = moment().format("MM/DD/YYYY");
const h = moment().format("DD/MM/YYYY");
const i = moment().format("yyyy/MM/dd");
const j = "\u20ac" + amount;
const k = "\u00a3" + total;
const l = return "$" + price;
`
	warnings := checkI18n("test.js", "", content)
	if len(warnings) == 0 {
		t.Fatal("expected warnings, got none")
	}
	// Should include header + capped warnings + truncation notice
	foundTruncation := false
	for _, w := range warnings {
		if strings.Contains(w, "truncated") {
			foundTruncation = true
			break
		}
	}
	if !foundTruncation {
		t.Errorf("expected truncation notice when exceeding maxI18nWarnings (%d), got %d warnings", maxI18nWarnings, len(warnings))
	}
}

func TestCheckI18n_CleanCodeNoWarning(t *testing.T) {
	content := `import { useTranslation } from 'react-i18next';
function MyComponent() {
  const { t } = useTranslation();
  const date = new Date().toLocaleDateString('en-US');
  const price = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' });
  return <button>{t('submit')}</button>;
}`
	warnings := checkI18n("component.tsx", "", content)
	if len(warnings) > 0 {
		t.Errorf("expected no warnings for clean i18n code, got %v", warnings)
	}
}
