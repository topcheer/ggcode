package tool

import "testing"

func TestIsAllowedNavScheme(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/page", true},
		{"http://localhost:3000/", true},
		{"HTTP://EXAMPLE.COM", true},
		{"file:///Users/x/.ssh/id_rsa", false},
		{"file:///etc/passwd", false},
		{"chrome://settings", false},
		{"chrome://version", false},
		{"about:blank", false},
		{"data:text/html,<script>1</script>", false},
		{"javascript:alert(1)", false},
		{"ftp://example.com/file", false},
		{"/Users/x/relative", false},
		{"example.com/page", false},
		{"", false},
		{"http://", false},
		{"https://ex.com\n/../..", false},
	}
	for _, c := range cases {
		if got := isAllowedNavScheme(c.url); got != c.want {
			t.Errorf("isAllowedNavScheme(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}
