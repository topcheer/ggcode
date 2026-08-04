package commands

import (
	"strconv"
	"strings"
)

// CompareVersions compares two semantic version strings (e.g. "1.2.3").
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Missing segments are treated as 0. Non-numeric segments are ignored
// (compared as 0). Empty strings are treated as "0".
func CompareVersions(a, b string) int {
	av := parseVersionParts(a)
	bv := parseVersionParts(b)
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
	return 0
}

func parseVersionParts(v string) []int {
	v = strings.TrimSpace(v)
	// Strip leading 'v' or 'V'
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	// Take only the part before any '-' (pre-release) or '+' (build)
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
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
	return out
}

// DependencyConstraint represents a parsed skill dependency with an optional
// version constraint, e.g. "my-skill@>=1.0.0" or "my-skill@2.0".
type DependencyConstraint struct {
	Name    string
	Op      string // "", ">=", ">", "<=", "<", "==", "="
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
	for _, candidate := range []string{">=", "<=", "==", ">", "<", "="} {
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
	default: // "==", "=", ""
		return cmp == 0
	}
}
