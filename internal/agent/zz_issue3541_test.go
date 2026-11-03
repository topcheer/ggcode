package agent

import "testing"

func TestIssue3541_LegitChecksumNotFlagged(t *testing.T) {
	for _, src := range []string{
		"digest = hashlib.md5(content).hexdigest()  # file checksum",
		"cache_key = hashlib.sha1(blob).hexdigest()",
		"dedup = hashlib.md5(chunk).digest()  # content-addressed storage",
	} {
		if got := findInsecurePatternsPython(src); len(got) != 0 {
			t.Fatalf("legit non-security MD5/SHA1 flagged (%q): %+v", src, got)
		}
	}
}

func TestIssue3541_PasswordHashingStillFlagged(t *testing.T) {
	for _, src := range []string{
		"hashed = hashlib.md5(password.encode()).hexdigest()",
		"h = hashlib.sha1(token).hexdigest()",
		"digest = hashlib.md5(secret_bytes).hexdigest()",
	} {
		found := false
		for _, inst := range findInsecurePatternsPython(src) {
			if inst.category == "weak crypto" {
				found = true
			}
		}
		if !found {
			t.Fatalf("genuine security-context weak hash NOT flagged (%q)", src)
		}
	}
}
