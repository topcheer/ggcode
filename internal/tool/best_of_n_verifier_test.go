package tool

import (
	"testing"
)

func TestValidateVerifierModels(t *testing.T) {
	avail := func() []string { return []string{"glm-5.3", "glm-5.3-flash", "glm-5.3-air"} }
	cases := []struct {
		name   string
		models []string
		fn     func() []string
		err    bool
	}{
		{"empty ok", nil, avail, false},
		{"single available", []string{"glm-5.3-flash"}, avail, false},
		{"several available", []string{"glm-5.3-flash", "glm-5.3-air"}, avail, false},
		{"unavailable model refused", []string{"gpt-99"}, avail, true},
		{"too many refused", []string{"a", "b", "c", "d", "e"}, avail, true},
		{"nil availability disables check", []string{"anything"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateVerifierModels(c.models, c.fn)
			if (got != "") != c.err {
				t.Fatalf("err=%v got %q", c.err, got)
			}
		})
	}
}
