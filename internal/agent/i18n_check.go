package agent

// Internationalization (i18n) Intelligence Check
//
// Research basis: i18n shift-left is a 2025-2026 industry trend driven by
// EU Digital Sovereignty requirements, expanding Chinese/Japanese/Arabic
// markets, and the cost of late-stage localization rework. Tools like
// i18next, FormatJS, linguiJS, and Globalize.js provide runtime i18n
// infrastructure, but no AI coding agent detects i18n anti-patterns at
// WRITE TIME -- before the code reaches QA or production.
//
// Competitor analysis:
//   - GitHub Copilot: occasional inline hints via extensions, inconsistent
//   - Cursor: no built-in i18n detection
//   - Cline: no i18n detection
//   - Claude Code: relies on agent self-judgment (unreliable)
//   - i18n-ally (VS Code extension): good key extraction, but no anti-pattern
//     detection for locale-sensitive API misuse
//   - eslint-plugin-formatjs: catches some issues, but requires setup and
//     only works for FormatJS projects
//
// ggcode's approach: deterministic, zero-LLM-cost pattern detection at
// write time for JS/TS/JSX/TSX files. We focus on the highest-impact,
// lowest-false-positive i18n anti-patterns:
//
//   1. Locale-sensitive methods called WITHOUT locale argument:
//      toLocaleDateString(), toLocaleTimeString(), toLocaleString()
//      Without a locale arg, these use the runtime default locale, causing
//      inconsistent formatting across users in different regions.
//      (Mozilla/Google i18n best practices)
//
//   2. Intl.NumberFormat / Intl.DateTimeFormat called WITHOUT locale:
//      new Intl.NumberFormat() and new Intl.DateTimeFormat() without a
//      locale argument use the system default, producing locale-inconsistent
//      output.
//
//   3. Hardcoded date format strings:
//      Common non-localized date format patterns like "YYYY-MM-DD",
//      "MM/DD/YYYY", "%Y-%m-%d" in string literals. These should use
//      locale-aware date formatting instead.
//
//   4. Hardcoded currency symbols in string/format context:
//      Currency symbols ($, EUR, GBP, JPY, etc.) embedded in string literals
//      used for display. Currency formatting should use Intl.NumberFormat
//      with currency style or a dedicated i18n formatting function.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// --- Compiled patterns for performance ---

// bt is the backtick character, used to build regex strings that include
// backtick as a literal (Go raw string literals cannot contain backticks).
const bt = "`"

// localeMethodNoArgRe matches locale-sensitive method calls without arguments.
// Captures the method name in group 1.
var localeMethodNoArgRe = regexp.MustCompile(`\.(toLocaleDateString|toLocaleTimeString|toLocaleString)\(\s*\)`)

// intlFormatNoArgRe matches Intl.NumberFormat() and Intl.DateTimeFormat()
// called without any arguments.
var intlFormatNoArgRe = regexp.MustCompile(`Intl\.(NumberFormat|DateTimeFormat)\(\s*\)`)

// hardcodedDateFormatRe matches common hardcoded date format tokens in
// string literals. Covers moment.js, date-fns, strftime, and .NET style patterns.
var hardcodedDateFormatRe = regexp.MustCompile(
	`["'` + bt + `](YYYY[-/]MM[-/]DD|DD[-/]MM[-/]YYYY|MM[-/]DD[-/]YYYY|yyyy[-/]MM[-/]dd|dd[-/]MM[-/]yyyy|%Y[-/]%m[-/]%d|%d[-/]%m[-/]%Y|MM[/]DD[/]YYYY)`)

// currencyInLiteralRe matches non-dollar currency symbols embedded in string
// literals. Very low false-positive rate since these symbols rarely appear
// outside formatting context.
var currencyInLiteralRe = regexp.MustCompile(`["'` + bt + `][^"'` + bt + `]*[\x{20ac}\x{00a3}\x{00a5}\x{20b9}\x{20bd}\x{20a9}][^"'` + bt + `]*["'` + bt + `]`)

// dollarInFormatRe matches dollar signs used in string context that suggest
// currency formatting (e.g., "$" + price, return "$", or "$" prefix in template).
// Excludes template literal ${} interpolation syntax.
var dollarInFormatRe = regexp.MustCompile(`["']\$["']\s*\+|\+\s*["']\$["']|return\s+["']\$["']`)

// hardcodedGoTimeFormatRe matches Go time.Format() calls with hardcoded
// layout strings (Go-specific date format using reference time tokens).
var hardcodedGoTimeFormatRe = regexp.MustCompile(`\.Format\(\s*["'](?:2006|01|02|15|04|05)[a-zA-Z0-9\-_/:. ]+["']`)

const maxI18nWarnings = 8

// i18nCheckResult holds a single i18n issue found during scanning.
type i18nCheckResult struct {
	category string
	message  string
}

// checkI18n performs internationalization checks on JS/TS/Go content.
func checkI18n(filePath, _, content string) []string {
	ext := strings.ToLower(filepath.Ext(filePath))
	isJS := ext == ".js" || ext == ".jsx" || ext == ".ts" || ext == ".tsx" || ext == ".mjs" || ext == ".cjs"
	isGo := ext == ".go"

	if !isJS && !isGo {
		return nil
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}

	var results []i18nCheckResult

	if isJS {
		results = append(results, checkLocaleMethodNoArg(content)...)
		results = append(results, checkIntlFormatNoArg(content)...)
		results = append(results, checkHardcodedDateFormat(content)...)
		results = append(results, checkCurrencyInLiteral(content)...)
	}

	if isGo {
		results = append(results, checkGoHardcodedTimeFormat(content)...)
	}

	if len(results) == 0 {
		return nil
	}

	// Cap warnings
	if len(results) > maxI18nWarnings {
		extra := len(results) - maxI18nWarnings
		results = results[:maxI18nWarnings]
		results = append(results, i18nCheckResult{
			category: "truncated",
			message:  fmt.Sprintf("[%d more i18n issues truncated]", extra),
		})
	}

	warnings := []string{
		fmt.Sprintf("Internationalization (i18n) check found %d issue(s) in %s:", len(results), filepath.Base(filePath)),
	}
	for _, r := range results {
		warnings = append(warnings, "  - "+r.message)
	}
	return warnings
}

// checkLocaleMethodNoArg detects locale-sensitive methods called without
// a locale argument, causing runtime-default locale formatting.
func checkLocaleMethodNoArg(content string) []i18nCheckResult {
	matches := localeMethodNoArgRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var results []i18nCheckResult
	for _, m := range matches {
		method := m[1]
		if seen[method] {
			continue
		}
		seen[method] = true
		results = append(results, i18nCheckResult{
			category: "locale-method-no-arg",
			message: fmt.Sprintf(
				".%s() called without locale argument. This uses the runtime default locale, "+
					"producing inconsistent output across users in different regions. "+
					"Pass an explicit locale: .%s('en-US') or use Intl APIs.",
				method, method),
		})
	}
	return results
}

// checkIntlFormatNoArg detects Intl.NumberFormat() and Intl.DateTimeFormat()
// called without a locale argument.
func checkIntlFormatNoArg(content string) []i18nCheckResult {
	matches := intlFormatNoArgRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var results []i18nCheckResult
	for _, m := range matches {
		api := m[1]
		if seen[api] {
			continue
		}
		seen[api] = true
		results = append(results, i18nCheckResult{
			category: "intl-format-no-arg",
			message: fmt.Sprintf(
				"Intl.%s() called without locale argument. Uses system default locale. "+
					"Pass an explicit locale: new Intl.%s('en-US', { ... }).",
				api, api),
		})
	}
	return results
}

// checkHardcodedDateFormat detects hardcoded date format strings that should
// use locale-aware date formatting.
func checkHardcodedDateFormat(content string) []i18nCheckResult {
	matches := hardcodedDateFormatRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var results []i18nCheckResult
	for _, m := range matches {
		token := m[1]
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		results = append(results, i18nCheckResult{
			category: "hardcoded-date-format",
			message: fmt.Sprintf(
				"Hardcoded date format %q found. Date formats are locale-sensitive "+
					"(e.g., US uses MM/DD/YYYY, EU uses DD/MM/YYYY). Use Intl.DateTimeFormat "+
					"or a locale-aware date library instead.",
				token),
		})
	}
	return results
}

// checkCurrencyInLiteral detects hardcoded currency symbols in string literals.
func checkCurrencyInLiteral(content string) []i18nCheckResult {
	var results []i18nCheckResult

	// Non-dollar currency symbols (very low false-positive rate)
	currencyMatches := currencyInLiteralRe.FindAllString(content, -1)
	seenCurrency := make(map[string]bool)
	for _, m := range currencyMatches {
		if seenCurrency[m] {
			continue
		}
		seenCurrency[m] = true
		for _, sym := range []string{"\u20ac", "\u00a3", "\u00a5", "\u20b9", "\u20bd", "\u20a9"} {
			if strings.Contains(m, sym) {
				results = append(results, i18nCheckResult{
					category: "hardcoded-currency",
					message: fmt.Sprintf(
						"Hardcoded currency symbol %q found in string literal. Currency symbols "+
							"and positions vary by locale (e.g., '10 EUR' in French vs 'EUR 10' in German). "+
							"Use Intl.NumberFormat with style: 'currency' instead.",
						sym),
				})
				break
			}
		}
	}

	// Dollar sign in formatting context (concatenation or return)
	dollarMatches := dollarInFormatRe.FindAllString(content, -1)
	if len(dollarMatches) > 0 {
		results = append(results, i18nCheckResult{
			category: "hardcoded-currency",
			message: "Hardcoded '$' currency symbol in string context. " +
				"Use Intl.NumberFormat with style: 'currency', currency: 'USD' for " +
				"locale-aware currency formatting.",
		})
	}

	return results
}

// checkGoHardcodedTimeFormat detects Go time.Format() calls with hardcoded
// layout strings.
func checkGoHardcodedTimeFormat(content string) []i18nCheckResult {
	matches := hardcodedGoTimeFormatRe.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var results []i18nCheckResult
	for _, m := range matches {
		if seen[m] {
			continue
		}
		seen[m] = true
		results = append(results, i18nCheckResult{
			category: "hardcoded-time-format",
			message: "Hardcoded Go time.Format() layout string detected. " +
				"Date/time formats are locale-sensitive. Consider using locale-aware " +
				"formatting or centralizing format constants for consistency.",
		})
	}
	return results
}
