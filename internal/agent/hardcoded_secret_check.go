package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxSecretScanLen = 256 * 1024

var secretExemptExts = map[string]bool{
	".env":      true,
	".pem":      true,
	".key":      true,
	".p12":      true,
	".pfx":      true,
	".crt":      true,
	".cer":      true,
	".pub":      true,
	".asc":      true,
	".gpg":      true,
	".jks":      true,
	".keystore": true,
}

var secretExemptDirs = []string{
	"testdata", "fixtures", "test-fixtures", "mocks", "__mocks__",
}

func pathHasSegment(path, name string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == name {
			return true
		}

		if strings.HasSuffix(seg, "_"+name) {
			return true
		}
	}
	return false
}

func checkHardcodedSecrets(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if secretExemptExts[ext] {
		return nil
	}

	lowerPath := strings.ToLower(filePath)
	for _, dir := range secretExemptDirs {
		if pathHasSegment(lowerPath, dir) {
			return nil
		}
	}

	if strings.HasSuffix(lowerPath, "_test.go") {
		return nil
	}

	base := strings.ToLower(filepath.Base(filePath))
	if strings.HasPrefix(base, ".env") {
		return nil
	}

	const scanOverlap = 64

	scanNew := newContent
	if len(scanNew) > maxSecretScanLen+scanOverlap {

		scanNew = scanNew[:maxSecretScanLen+scanOverlap]
	}
	scanOld := oldContent
	if len(scanOld) > maxSecretScanLen+scanOverlap {
		scanOld = scanOld[:maxSecretScanLen+scanOverlap]
	}

	isSourceCode := isSourceCodeFile(filePath)

	var warnings []string

	for _, sp := range secretPatterns {

		if sp.name == "assignment_secret" && !isSourceCode {
			continue
		}

		oldMatches := sp.pattern.FindAllString(scanOld, -1)
		newMatches := sp.pattern.FindAllString(scanNew, -1)

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
