package agent

// Hardcoded Credential Detection in File Writes
//
// Research basis: OWASP LLM Top 10 2025 (#2: Sensitive Information Disclosure)
// and OWASP Top 10 A02:2021 (Cryptographic Failures). AI coding agents
// frequently introduce hardcoded credentials into source code — e.g.,
// writing `apiKey := "AKIAIOSFODNN7EXAMPLE"` or `const token = "ghp_xxxx"`
// as a "working example" or test fixture. Once committed, these credentials
// become permanent security vulnerabilities.
//
// This is fundamentally DIFFERENT from secret_redact.go:
//   - secret_redact.go: masks secrets in tool OUTPUT going to the LLM
//     (data-exfiltration prevention, runtime data flow)
//   - THIS module: detects secrets being WRITTEN to source files on disk
//     (vulnerability prevention, code-quality/data-at-rest flow)
//
// Competitor analysis:
//   - GitHub Copilot: has built-in secret scanning on suggestions
//   - Cursor: warns about hardcoded secrets via lint integration
//   - gitleaks/truffleHog: pre-commit secret scanning (external tools)
//   - Claude Code: relies on agent self-judgment (unreliable)
//   - Cline/OpenHands: no write-time secret detection
//
// ggcode's approach: reuse the high-precision patterns from secret_redact.go
// but apply them to file CONTENT DELTAS — only flag secrets INTRODUCED by
// this edit (present in newContent but not oldContent). This avoids false
// positives on pre-existing secrets and focuses on NEW vulnerabilities
// created by the agent's edit. The check is zero-LLM-cost and <1ms per file.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxSecretScanLen caps the content size scanned for hardcoded secrets.
// Files larger than 256KB are unlikely to have secrets introduced by a
// single edit, and scanning wastes CPU.
const maxSecretScanLen = 256 * 1024

// secretExemptExts lists file extensions where hardcoded credential-like
// strings are expected and should NOT trigger warnings.
var secretExemptExts = map[string]bool{
	".env":      true, // .env files are SUPPOSED to contain secrets
	".pem":      true, // certificate/key files
	".key":      true, // private key files
	".p12":      true, // keystore files
	".pfx":      true, // certificate exchange files
	".crt":      true, // certificate files
	".cer":      true, // certificate files
	".pub":      true, // public key files (not secrets)
	".asc":      true, // PGP keys
	".gpg":      true, // GPG keys
	".jks":      true, // Java keystore
	".keystore": true,
}

// secretExemptDirs lists directory-name segments where mock credentials
// are intentional (test fixtures, mocks). Fix #247: the previous list also
// exempted `secrets/`, `credentials/` and `.secrets/` — PRODUCTION directory
// names — silently hiding real AWS keys written to internal/credentials/.
// Only test-semantics directory names belong here.
var secretExemptDirs = []string{
	"testdata", "fixtures", "test-fixtures", "mocks", "__mocks__",
}

// pathHasSegment reports whether any path segment (or a *_test/fixtures-style
// suffixed segment like "foo_testdata") equals name. Fix #247: the previous
// substring match (`strings.Contains(lowerPath, "fixtures/")`) both missed
// trailing segments without a slash and matched unrelated paths like
// `myfixturesnote/`. Segment-exact comparison fixes both.
func pathHasSegment(path, name string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == name {
			return true
		}
		// Suffix forms: "config_testdata" / "api_fixtures".
		if strings.HasSuffix(seg, "_"+name) {
			return true
		}
	}
	return false
}

// checkHardcodedSecrets detects credential/secret values that were INTRODUCED
// by this edit (present in newContent but not in oldContent). Returns warning
// strings for each newly-introduced secret type.
//
// Key design decisions:
//   - Only flags NEW secrets: compares occurrence counts. Pre-existing secrets
//     in oldContent are not flagged (the agent didn't introduce them).
//   - Skips .env/.pem/.key files where secrets are expected.
//   - Skips test fixture directories where mock credentials are intentional.
//   - Uses the same high-precision patterns as secret_redact.go for consistency.
//   - Assignment-style secrets (apiKey=...) are only flagged in source code
//     files, not in config files (.yaml, .json, .toml, .ini) where they may
//     be legitimate configuration.
func checkHardcodedSecrets(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Skip files where secrets are expected
	ext := strings.ToLower(filepath.Ext(filePath))
	if secretExemptExts[ext] {
		return nil
	}

	// Skip test fixture directories (segment-exact, fix #247)
	lowerPath := strings.ToLower(filePath)
	for _, dir := range secretExemptDirs {
		if pathHasSegment(lowerPath, dir) {
			return nil
		}
	}

	// Also skip .env.example, .env.local, etc. (basename starts with ".env")
	base := strings.ToLower(filepath.Base(filePath))
	if strings.HasPrefix(base, ".env") {
		return nil
	}

	// Cap scan length for CPU protection. Fix #247: keep a 64-byte overlap
	// (>= the longest secret pattern length) across the truncation boundary on
	// both sides — old keeps the tail end of its prefix, new keeps a head
	// extension — so a secret straddling the 256KB cut is not silently halved
	// on one side only, which made the old/new multiset diff unreliable.
	const scanOverlap = 64
	scanNew := newContent
	if len(scanNew) > maxSecretScanLen {
		scanNew = scanNew[:maxSecretScanLen+scanOverlap]
	}
	scanOld := oldContent
	if len(scanOld) > maxSecretScanLen {
		// Keep the TAIL of oldContent so a secret near the end of the old
		// file is still visible to the diff even after truncation.
		scanOld = oldContent[len(oldContent)-(maxSecretScanLen+scanOverlap):]
	}

	// Determine if this is a source code file (vs config/docs).
	// Assignment-style secrets are only flagged in source code to avoid
	// false positives in legitimate config files.
	isSourceCode := isSourceCodeFile(filePath)

	var warnings []string

	for _, sp := range secretPatterns {
		// Skip assignment_secret pattern for non-source files
		if sp.name == "assignment_secret" && !isSourceCode {
			continue
		}

		// For patterns with capture groups, count the full matches
		oldMatches := sp.pattern.FindAllString(scanOld, -1)
		newMatches := sp.pattern.FindAllString(scanNew, -1)

		// Per-instance set comparison (fix #171): count-diff (newCount-oldCount)
		// is blind to remove-N-add-N edits — swapping a placeholder key for a
		// REAL credential of the same pattern family keeps the count unchanged
		// and passes silently. Count only new matches whose exact text is absent
		// from the old multiset.
		oldSet := make(map[string]int)
		for _, m := range oldMatches {
			oldSet[m]++
		}
		introduced := 0
		for _, m := range newMatches {
			oldSet[m]--
			if oldSet[m] < 0 {
				introduced++
			}
		}
		if introduced > 0 {
			warnings = append(warnings, formatSecretWarning(sp.name, introduced))
		}
	}

	if len(warnings) > 0 {
		debug.Log("hardcoded-secret", "detected %d secret type(s) introduced in %s", len(warnings), filePath)
	}

	return warnings
}

// (isSourceCodeFile is defined in verify_hint.go and reused here.)

// formatSecretWarning renders a concise warning for a newly-introduced secret.
func formatSecretWarning(secretType string, count int) string {
	noun := "instance"
	if count > 1 {
		noun = "instances"
	}
	return fmt.Sprintf(
		"[SECURITY WARNING] Detected %d new %s %s hardcoded in this file. "+
			"Hardcoded credentials are a critical security vulnerability (OWASP A02:2021). "+
			"Move secrets to environment variables, a secrets manager, or a .env file "+
			"(excluded from version control). Remove the hardcoded value and use "+
			"os.Getenv() or equivalent to read it at runtime.",
		count, secretType, noun)
}
