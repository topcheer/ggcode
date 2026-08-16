package commands

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.0", 1},
		{"2.0", "1.9.9", 1},
		{"1.0.0", "2.0.0", -1},
		{"v1.2.3", "1.2.3", 0},
		{"V1.2.3", "1.2.3", 0},
		{"1.0.0-beta", "1.0.0", -1}, // pre-release < release (#536)
		{"1.0.0+build1", "1.0.0", 0},
		{"", "0.0.0", 0},
		{"1", "1.0.0", 0},
		{"1.2", "1.2.0", 0},
		{"1.10.0", "1.9.0", 1},
	}
	for _, tt := range tests {
		got := CompareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseDependency(t *testing.T) {
	tests := []struct {
		raw     string
		name    string
		op      string
		version string
	}{
		{"foo", "foo", "", ""},
		{"foo@>=1.0.0", "foo", ">=", "1.0.0"},
		{"foo@>1.0", "foo", ">", "1.0"},
		{"foo@<=2.0", "foo", "<=", "2.0"},
		{"foo@<3.0.0", "foo", "<", "3.0.0"},
		{"foo@==1.5.0", "foo", "==", "1.5.0"},
		{"foo@1.2.3", "foo", "==", "1.2.3"},
		{"foo@=2.0", "foo", "=", "2.0"},
		{"  bar@>=1.0  ", "bar", ">=", "1.0"},
	}
	for _, tt := range tests {
		dc := ParseDependency(tt.raw)
		if dc.Name != tt.name || dc.Op != tt.op || dc.Version != tt.version {
			t.Errorf("ParseDependency(%q) = {Name:%q Op:%q Version:%q}, want {Name:%q Op:%q Version:%q}",
				tt.raw, dc.Name, dc.Op, dc.Version, tt.name, tt.op, tt.version)
		}
	}
}

func TestCheckVersionConstraint(t *testing.T) {
	tests := []struct {
		actual   string
		op       string
		required string
		want     bool
	}{
		// No required version: always satisfied
		{"1.0.0", "", "", true},
		{"", "", "", true},
		// Exact match
		{"1.0.0", "==", "1.0.0", true},
		{"1.0.0", "=", "1.0.0", true},
		{"1.0.1", "==", "1.0.0", false},
		// Greater than or equal
		{"1.0.0", ">=", "1.0.0", true},
		{"1.0.1", ">=", "1.0.0", true},
		{"0.9.0", ">=", "1.0.0", false},
		// Greater than
		{"1.0.1", ">", "1.0.0", true},
		{"1.0.0", ">", "1.0.0", false},
		// Less than or equal
		{"1.0.0", "<=", "1.0.0", true},
		{"0.9.0", "<=", "1.0.0", true},
		{"1.0.1", "<=", "1.0.0", false},
		// Less than
		{"0.9.0", "<", "1.0.0", true},
		{"1.0.0", "<", "1.0.0", false},
		// No actual version: cannot satisfy any constraint
		{"", ">=", "1.0.0", false},
		{"", "==", "1.0.0", false},
	}
	for _, tt := range tests {
		got := CheckVersionConstraint(tt.actual, tt.op, tt.required)
		if got != tt.want {
			t.Errorf("CheckVersionConstraint(%q, %q, %q) = %v, want %v",
				tt.actual, tt.op, tt.required, got, tt.want)
		}
	}
}
