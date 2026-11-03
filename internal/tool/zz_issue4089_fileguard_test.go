package tool

import "testing"

func TestMatchDoubleStarNoPrefixMultiSegmentSuffix(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{

		{"**/secrets/**", "src/secrets/key.pem", true},
		{"**/secrets/**", "secrets/key.pem", true},
		{"**/secrets/**", "a/b/secrets/c/d.pem", true},
		{"**/secrets/**", "src/other/key.pem", false},
		{"**/secrets/**", "src/xsecrets/key.pem", false},

		{"**/secrets/**", "src/secrets", true},

		{"**/config/prod.yaml", "src/config/prod.yaml", true},
		{"**/config/prod.yaml", "config/prod.yaml", true},
		{"**/config/prod.yaml", "src/config/dev.yaml", false},

		{"**/*.pem", "src/secrets/key.pem", true},
		{"**/*.pem", "src/secrets/key.txt", false},
	}
	for _, c := range cases {
		got, err := matchDoubleStar(c.pattern, c.name)
		if err != nil {
			t.Errorf("matchDoubleStar(%q, %q) error: %v", c.pattern, c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("matchDoubleStar(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
