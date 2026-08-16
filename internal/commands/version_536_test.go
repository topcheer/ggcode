package commands

import "testing"

// #536 Bug A: pre-release versions must rank below their release version
// per SemVer §11 — previously the tag was stripped, making 1.0.0-rc1 == 1.0.0
// so a ">=1.0.0" constraint was falsely satisfied by an rc build.
func TestCompareVersions_PrereleaseRanksBelowRelease(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc1", "1.0.0-rc1", 0},
		{"1.0.0-alpha", "1.0.0-beta", -1},   // lexical compare
		{"1.0.0-rc1", "1.0.0-rc2", -1},      // numeric compare
		{"1.0.0-1", "1.0.0-alpha", -1},      // numeric < alphanumeric
		{"1.0.0-alpha.1", "1.0.0-alpha", 1}, // more identifiers ranks higher
		{"1.0.0+build1", "1.0.0", 0},        // build metadata ignored
		{"1.0.0-rc1+build.9", "1.0.0-rc1", 0},
		{"1.0.0-rc1", "1.0.1", -1}, // core version dominates
		{"v1.0.0-rc.1", "1.0.0", -1},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// #536 Bug A corollary: an rc build must NOT satisfy ">=1.0.0".
func TestCheckVersionConstraint_PrereleaseNotGE(t *testing.T) {
	if CheckVersionConstraint("1.0.0-rc1", ">=", "1.0.0") {
		t.Error("1.0.0-rc1 must not satisfy >=1.0.0")
	}
	if !CheckVersionConstraint("1.0.0", ">=", "1.0.0-rc1") {
		t.Error("1.0.0 must satisfy >=1.0.0-rc1")
	}
}

// #536 Bug B: ^, ~, and != operators must be parsed and honored instead of
// silently degrading to "==0.x" (Atoi("^1.2.0") fails -> 0).
func TestParseDependency_CaretTildeNeq(t *testing.T) {
	tests := []struct {
		raw, name, op, version string
	}{
		{"foo@^1.2.0", "foo", "^", "1.2.0"},
		{"foo@~2.3", "foo", "~", "2.3"},
		{"foo@!=1.0.0", "foo", "!=", "1.0.0"},
		{"  bar@^0.2.0  ", "bar", "^", "0.2.0"},
	}
	for _, tt := range tests {
		dc := ParseDependency(tt.raw)
		if dc.Name != tt.name || dc.Op != tt.op || dc.Version != tt.version {
			t.Errorf("ParseDependency(%q) = {Name:%q Op:%q Version:%q}, want {Name:%q Op:%q Version:%q}",
				tt.raw, dc.Name, dc.Op, dc.Version, tt.name, tt.op, tt.version)
		}
	}
}

func TestCheckVersionConstraint_Caret(t *testing.T) {
	tests := []struct {
		actual, required string
		want             bool
	}{
		// ^1.2.0 := >=1.2.0 <2.0.0
		{"1.2.0", "^1.2.0", true},
		{"1.5.9", "^1.2.0", true},
		{"1.9.9", "^1.2.0", true},
		{"2.0.0", "^1.2.0", false},
		{"1.1.9", "^1.2.0", false},
		// ^0.2.0 := >=0.2.0 <0.3.0 (leading zero bumps next segment)
		{"0.2.0", "^0.2.0", true},
		{"0.2.7", "^0.2.0", true},
		{"0.3.0", "^0.2.0", false},
		{"0.1.9", "^0.2.0", false},
		// ^0.0.3 := >=0.0.3 <0.0.4
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
	}
	for _, tt := range tests {
		if got := CheckVersionConstraint(tt.actual, "^", tt.required); got != tt.want {
			t.Errorf("CheckVersionConstraint(%q, ^, %q) = %v, want %v", tt.actual, tt.required, got, tt.want)
		}
	}
}

func TestCheckVersionConstraint_Tilde(t *testing.T) {
	tests := []struct {
		actual, required string
		want             bool
	}{
		// ~1.2.0 := >=1.2.0 <1.3.0
		{"1.2.0", "~1.2.0", true},
		{"1.2.9", "~1.2.0", true},
		{"1.3.0", "~1.2.0", false},
		{"1.1.0", "~1.2.0", false},
		// ~1.2 (major.minor only) := >=1.2.0 <1.3.0
		{"1.2.5", "~1.2", true},
		{"1.3.0", "~1.2", false},
		// ~1 (major only) := >=1.0.0 <2.0.0
		{"1.9.0", "~1", true},
		{"2.0.0", "~1", false},
	}
	for _, tt := range tests {
		if got := CheckVersionConstraint(tt.actual, "~", tt.required); got != tt.want {
			t.Errorf("CheckVersionConstraint(%q, ~, %q) = %v, want %v", tt.actual, tt.required, got, tt.want)
		}
	}
}

func TestCheckVersionConstraint_Neq(t *testing.T) {
	if CheckVersionConstraint("1.0.0", "!=", "1.0.0") {
		t.Error("1.0.0 must not satisfy !=1.0.0")
	}
	if !CheckVersionConstraint("1.0.1", "!=", "1.0.0") {
		t.Error("1.0.1 must satisfy !=1.0.0")
	}
}
