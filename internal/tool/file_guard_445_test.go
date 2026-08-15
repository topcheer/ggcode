package tool

import "testing"

// #445: glob metacharacters in the prefix before ** must expand.
func TestMatchDoubleStarGlobPrefix(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"src/*-gen/**", "src/a-gen/secret.key", true},
		{"src/*-gen/**", "src/a-gen/deep/x.key", true},
		{"src/*-gen/**", "src/a/secret.key", false},
		{"src/[ab]/**", "src/a/x", true},
		{"src/[ab]/**", "src/c/x", false},
		{"src/**", "src/anything/deep/file", true},
		{"src/**/test", "src/a/b/test", true},
		{"**", "any/path", true},
	}
	for _, c := range cases {
		got, err := matchDoubleStar(c.pattern, c.name)
		if err != nil {
			t.Fatalf("pattern %q: %v", c.pattern, err)
		}
		if got != c.want {
			t.Errorf("matchDoubleStar(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
