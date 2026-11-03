package tool

import "testing"

func Test1703BlockPrivateHost(t *testing.T) {
	blocked := []string{
		"localhost",
		"127.0.0.1", "::1", "0.0.0.0",
		"10.0.0.5", "192.168.1.4", "172.16.2.3",
		"169.254.169.254",
	}
	for _, h := range blocked {
		if !isPrivateHost(h) {
			t.Errorf("private host not blocked: %s", h)
		}
	}
	allowed := []string{"example.com", "8.8.8.8", "1.1.1.1"}
	for _, h := range allowed {
		if isPrivateHost(h) {
			t.Errorf("public host wrongly blocked: %s", h)
		}
	}
}

func Test1703XargsRBoundary(t *testing.T) {

	m := shellCompatPatterns
	for _, r := range m {
		if r.match("grep -r pattern . | xargs ls", "") {
			t.Fatal("grep -r | xargs misfires (flag belongs to grep)")
		}
	}
	hit := false
	for _, r := range m {
		if r.match("find . -name '*.go' | xargs -r wc -l", "") {
			hit = true
		}
	}
	if !hit {
		t.Fatal("real xargs -r no longer detected")
	}
}

func Test1703PrefixAnchoring(t *testing.T) {
	for _, r := range shellCompatPatterns {

		if r.match("sort_versions.sh -v 1.2 1.3", "") {
			t.Fatal("sort_versions.sh tripped the sort rule (bare-prefix match)")
		}
	}

	for _, cmd := range []string{
		"readlink -f p", "stat -c %s f",
	} {
		hit := false
		for _, r := range shellCompatPatterns {
			if r.match(cmd, "") {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("real invocation not detected: %s", cmd)
		}
	}

	for _, r := range shellCompatPatterns {
		if r.match("sort -k2 file | grep -v x", "") {
			t.Fatal("sort | grep -v misfired the version-sort rule")
		}
	}
}
