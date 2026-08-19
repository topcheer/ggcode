package tool

import "testing"

// #741: browser navigation must only allow http/https — file:// turns the
// browser into a sandbox-free local file reader.
func TestIsAllowedNavScheme(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/page", true},
		{"http://localhost:3000/", true},
		{"HTTP://EXAMPLE.COM", true},           // scheme is case-insensitive
		{"file:///Users/x/.ssh/id_rsa", false}, // the #741 attack
		{"file:///etc/passwd", false},
		{"chrome://settings", false},
		{"chrome://version", false},
		{"about:blank", false},
		{"data:text/html,<script>1</script>", false},
		{"javascript:alert(1)", false},
		{"ftp://example.com/file", false},
		{"/Users/x/relative", false},      // schemeless
		{"example.com/page", false},       // schemeless
		{"", false},                       // empty
		{"http://", false},                // scheme but no host
		{"https://ex.com\n/../..", false}, // control chars -> parse handles
	}
	for _, c := range cases {
		if got := isAllowedNavScheme(c.url); got != c.want {
			t.Errorf("isAllowedNavScheme(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
