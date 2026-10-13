package tool

// #4089: matchDoubleStar's prefix=="" branch matched the suffix only
// against filepath.Base(name). A multi-segment suffix ('**/secrets/**',
// '**/config/prod.yaml') contains '/' and can never equal a single base
// segment, so those no-prefix protection patterns silently failed OPEN.
import "testing"

func TestMatchDoubleStarNoPrefixMultiSegmentSuffix(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		// The issue's exact repro: protection silently allowed.
		{"**/secrets/**", "src/secrets/key.pem", true},
		{"**/secrets/**", "secrets/key.pem", true},
		{"**/secrets/**", "a/b/secrets/c/d.pem", true},
		{"**/secrets/**", "src/other/key.pem", false},
		{"**/secrets/**", "src/xsecrets/key.pem", false},
		// The dir itself matches, consistent with prefixed 'secrets/**'
		// semantics (matchSegFS("**", "secrets") is true there).
		{"**/secrets/**", "src/secrets", true},
		// Full-path fixed tail.
		{"**/config/prod.yaml", "src/config/prod.yaml", true},
		{"**/config/prod.yaml", "config/prod.yaml", true},
		{"**/config/prod.yaml", "src/config/dev.yaml", false},
		// Single-segment suffixes keep the pre-#4089 base behavior.
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
