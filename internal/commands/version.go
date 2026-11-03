package commands

import (
	"strconv"
	"strings"
)

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

func parseVersionParts(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
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
		if ai == "" {
			return -1
		}
		if bi == "" {
			return 1
		}
		return comparePreReleaseIdent(ai, bi)
	}
	if len(as) < len(bs) {
		return -1
	}
	return 1
}

func comparePreReleaseIdent(ai, bi string) int {
	an, aerr := strconv.Atoi(ai)
	bn, berr := strconv.Atoi(bi)
	switch {
	case aerr == nil && berr == nil:
		if an < bn {
			return -1
		}
		return 1
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	default:
		if ai < bi {
			return -1
		}
		return 1
	}
}

type DependencyConstraint struct {
	Name    string
	Op      string
	Version string
}

func ParseDependency(raw string) DependencyConstraint {
	raw = strings.TrimSpace(raw)
	idx := strings.Index(raw, "@")
	if idx < 0 {
		return DependencyConstraint{Name: raw}
	}
	name := strings.TrimSpace(raw[:idx])
	constraint := strings.TrimSpace(raw[idx+1:])
	dc := DependencyConstraint{Name: name}
	for _, candidate := range []string{">=", "<=", "!=", "==", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(constraint, candidate) {
			dc.Op = candidate
			dc.Version = strings.TrimSpace(constraint[len(candidate):])
			return dc
		}
	}
	dc.Op = "=="
	dc.Version = constraint
	return dc
}

func CheckVersionConstraint(actualVersion, op, requiredVersion string) bool {
	if requiredVersion == "" {
		return true
	}
	if actualVersion == "" {
		return false
	}
	switch op {
	case "^", "~", ">=", ">", "<=", "<":
		if !prereleaseRangeAllowed(actualVersion, requiredVersion) {
			return false
		}
	}
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
	default:
		return cmp == 0
	}
}

func prereleaseRangeAllowed(actual, required string) bool {
	required = strings.TrimPrefix(strings.TrimSpace(required), "^")
	required = strings.TrimPrefix(required, "~")
	_, apre := parseVersionParts(actual)
	if apre == "" {
		return true
	}
	rp, rpre := parseVersionParts(required)
	if rpre == "" {
		return false
	}
	ap, _ := parseVersionParts(actual)
	return sameCoreTuple(ap, rp)
}

func sameCoreTuple(a, b []int) bool {
	for i := 0; i < 3; i++ {
		var ai, bi int
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai != bi {
			return false
		}
	}
	return true
}

func inCaretTildeRange(actual, required string, caret bool) bool {
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
		if len(parts) >= 2 {
			bump = 1
		} else {
			bump = 0
		}
	} else {
		bump = 0
		for i, p := range parts {
			if p != 0 {
				bump = i
				break
			}
			bump = i
		}
	}
	upper := make([]int, len(parts))
	copy(upper, parts)
	upper[bump]++
	for i := bump + 1; i < len(upper); i++ {
		upper[i] = 0
	}
	return CompareVersions(actual, versionString(upper)) < 0
}

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
