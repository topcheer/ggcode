package tool

import "testing"

// #1703 case 1: private/loopback skill-import targets are blocked
// (main landed isPrivateHost + a DNS-resolve rebinding guard; this pin
// covers the literal-host layer).
func Test1703BlockPrivateHost(t *testing.T) {
	blocked := []string{
		"localhost",
		"127.0.0.1", "::1", "0.0.0.0",
		"10.0.0.5", "192.168.1.4", "172.16.2.3",
		"169.254.169.254", // cloud metadata endpoint
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

// #1703 case 2: xargs -r must be a token of the xargs segment, not any
// " -r" anywhere in the pipeline.
func Test1703XargsRBoundary(t *testing.T) {
	// The exact false positive from the issue: -r belongs to grep.
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

// #1703 cases 3-7: bare prefixes must not match longer command names.
func Test1703PrefixAnchoring(t *testing.T) {
	for _, r := range shellCompatPatterns {
		// sort_versions.sh -v ... must not trip the sort rule.
		if r.match("sort_versions.sh -v 1.2 1.3", "") {
			t.Fatal("sort_versions.sh tripped the sort rule (bare-prefix match)")
		}
	}
	// Real invocations still detected.
	for _, cmd := range []string{
		"sort -V file.txt", "readlink -f p", "stat -c %s f",
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
	// sort with a later -v token (grep -v in a pipe) must not fire.
	for _, r := range shellCompatPatterns {
		if r.match("sort -k2 file | grep -v x", "") {
			t.Fatal("sort | grep -v misfired the version-sort rule")
		}
	}
}
