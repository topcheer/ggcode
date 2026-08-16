package commands

import (
	"strconv"
	"strings"
)

// CompareVersions compares two version strings (e.g. "1.2.3") using
// SemVer rules. Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Missing segments are treated as 0. Non-numeric segments are ignored
// (compared as 0). Empty strings are treated as "0".
// #536: build metadata (+...) is ignored and pre-release (-...) ranks
// below the same release version per SemVer §11 (1.0.0-rc1 < 1.0.0).
func CompareVersions(a, b string) int {
	av, apre := parseVersionParts(a)
	bv, bpre := parseVersionParts(b)
	max := len(av)
	if len(bv) > max {
		max = len(bv)
	}
	for i := 0; i < max; i++ {
		ai, bi := 0, 0
		if i < len(av) {
			ai = av[i]
		}
		if i < len(bv) {
			bi = bv[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	// Core versions equal: pre-release ranking (SemVer §11).
	// A version WITH a pre-release tag sorts BEFORE the plain release.
	if apre == "" && bpre != "" {
		return 1
	}
	if apre != "" && bpre == "" {
		return -1
	}
	if apre == "" && bpre == "" {
		return 0
	}
	return comparePreRelease(apre, bpre)
}

// parseVersionParts splits v into numeric core segments plus its pre-release
// identifier (the part after '-' with build metadata '+...' stripped).
func parseVersionParts(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	// Strip leading 'v' or 'V'
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Strip build metadata ('+...') first — ignored per SemVer §10.
	if idx := strings.IndexByte(v, '+'); idx >= 0 {
		v = v[:idx]
	}
	pre := ""
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		pre = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		n, err := strconv.Atoi(part)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out, pre
}

// comparePreRelease compares two pre-release identifiers per SemVer §11:
// dot-separated identifiers; numeric identifiers compare numerically,
// alphanumeric lexically, numeric < alphanumeric; a longer identifier list
// with equal prefix ranks higher (1.0.0-alpha.1 > 1.0.0-alpha).
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	max := len(as)
	if len(bs) > max {
		max = len(bs)
	}
	for i := 0; i < max; i++ {
		var ai, bi string
		if i < len(as) {
			ai = as[i]
		}
		if i < len(bs) {
			bi = bs[i]
		}
		if ai == bi {
			continue
		}
		// Equal prefix so far; a missing identifier means the SHORTER set
		// ranks lower (1.0.0-alpha < 1.0.0-alpha.1). comparePreReleaseIdent
		// treats "" as a lexical identifier, so guard here, not there.
		if ai == "" {
			return -1
		}
		if bi == "" {
			return 1
		}
		return comparePreReleaseIdent(ai, bi)
	}
	// Equal prefix so far; a missing identifier means the SHORTER set
	// ranks lower (1.0.0-alpha < 1.0.0-alpha.1).
	if len(as) < len(bs) {
		return -1
	}
	return 1
}

// comparePreReleaseIdent orders two pre-release identifiers per SemVer §11:
// numeric identifiers compare numerically, numeric < alphanumeric,
// alphanumeric lexically. Callers must handle the empty (missing) identifier
// case before reaching here — comparePreReleaseIdent treats "" as a plain
// lexical identifier.
func comparePreReleaseIdent(ai, bi string) int {
	an, aerr := strconv.Atoi(ai)
	bn, berr := strconv.Atoi(bi)
	switch {
	case aerr == nil && berr == nil: // both numeric
		if an < bn {
			return -1
		}
		return 1
	case aerr == nil: // numeric < alphanumeric
		return -1
	case berr == nil:
		return 1
	default: // both alphanumeric: lexical
		if ai < bi {
			return -1
		}
		return 1
	}
}

// DependencyConstraint represents a parsed skill dependency with an optional
// version constraint, e.g. "my-skill@>=1.0.0" or "my-skill@2.0".
type DependencyConstraint struct {
	Name    string
	Op      string // "", ">=", ">", "<=", "<", "==", "=", "^", "~", "!="
	Version string
}

// ParseDependency parses a dependency string that may include a version
// constraint after '@'. Examples:
//
//	"foo"           -> {Name: "foo"}
//	"foo@>=1.0"     -> {Name: "foo", Op: ">=", Version: "1.0"}
//	"foo@1.2.3"     -> {Name: "foo", Op: "==", Version: "1.2.3"}
func ParseDependency(raw string) DependencyConstraint {
	raw = strings.TrimSpace(raw)
	idx := strings.Index(raw, "@")
	if idx < 0 {
		return DependencyConstraint{Name: raw}
	}
	name := strings.TrimSpace(raw[:idx])
	constraint := strings.TrimSpace(raw[idx+1:])
	dc := DependencyConstraint{Name: name}
	// #536: "^"/"~/"!=" must be recognized BEFORE ">"/"<"/"=" so they don't
	// degrade into a bogus "==0.x" (Atoi("^1.2.0")→0) with no warning.
	for _, candidate := range []string{">=", "<=", "!=", "==", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(constraint, candidate) {
			dc.Op = candidate
			dc.Version = strings.TrimSpace(constraint[len(candidate):])
			return dc
		}
	}
	// No operator: exact match
	dc.Op = "=="
	dc.Version = constraint
	return dc
}

// CheckVersionConstraint returns true if actualVersion satisfies the
// constraint defined by op and requiredVersion. If requiredVersion is
// empty, any version (including none) satisfies.
func CheckVersionConstraint(actualVersion, op, requiredVersion string) bool {
	if requiredVersion == "" {
		return true
	}
	if actualVersion == "" {
		// No version declared, cannot satisfy a version constraint.
		return false
	}
	// #536: caret/tilde range semantics — a range check, not a point compare.
	if op == "^" || op == "~" {
		return inCaretTildeRange(actualVersion, requiredVersion, op == "^")
	}
	cmp := CompareVersions(actualVersion, requiredVersion)
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	case "!=":
		return cmp != 0
	default: // "==", "=", ""
		return cmp == 0
	}
}

// inCaretTildeRange implements "^ver" and "~ver" range checks.
//   - ^1.2.0 := >=1.2.0 <2.0.0   (first non-zero segment bumps)
//   - ^0.2.0 := >=0.2.0 <0.3.0   (zero first segment: bump second)
//   - ^0.0.3 := >=0.0.3 <0.0.4   (first two zero: bump third)
//   - ~1.2.0 := >=1.2.0 <1.3.0   (bump the minor segment)
//   - ~1.2   := >=1.2.0 <1.3.0   (same: minor specified, patch free)
//   - ~1     := >=1.0.0 <2.0.0   (only a major segment given: bump major)
func inCaretTildeRange(actual, required string, caret bool) bool {
	// Tolerate the operator itself embedded in requiredVersion (e.g. a caller
	// passing "^1.2.0" whole) — strip it before parsing.
	required = strings.TrimPrefix(strings.TrimSpace(required), "^")
	required = strings.TrimPrefix(required, "~")
	if CompareVersions(actual, required) < 0 {
		return false
	}
	parts, _ := parseVersionParts(required)
	if len(parts) == 0 {
		return true
	}
	bump := len(parts) - 1
	if !caret {
		// tilde: patch-level changes allowed when a minor is specified,
		// minor-level changes when only a major is given.
		if len(parts) >= 2 {
			bump = 1
		} else {
			bump = 0
		}
	} else {
		// caret: bump the leftmost non-zero segment
		bump = 0
		for i, p := range parts {
			if p != 0 {
				bump = i
				break
			}
			bump = i // all zero so far — keep advancing
		}
	}
	// Upper bound: increment the bumped segment and ZERO the tail, so
	// ^1.2.0 -> [2,0,0) not [2,2,0), ~1.2 -> [1,3,0) not [1,3).
	upper := make([]int, len(parts))
	copy(upper, parts)
	upper[bump]++
	for i := bump + 1; i < len(upper); i++ {
		upper[i] = 0
	}
	return CompareVersions(actual, versionString(upper)) < 0
}

// versionString renders numeric segments back to "x.y.z" form.
func versionString(parts []int) string {
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(strconv.Itoa(p))
	}
	return sb.String()
}
