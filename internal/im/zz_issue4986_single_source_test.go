package im

import (
	"os"
	"regexp"
	"testing"
)

// TestIssue4986SupportedPlatformsMatchesSwitchFace locks SupportedPlatforms()
// to the startConfiguredAdapter switch case face (single source of truth):
// adding a platform case without registering it in SupportedPlatforms (or
// vice versa) fails this probe.
func TestIssue4986SupportedPlatformsMatchesSwitchFace(t *testing.T) {
	src, err := os.ReadFile("adapters.go")
	if err != nil {
		t.Fatalf("read adapters.go: %v", err)
	}
	cases := regexp.MustCompile(`(?m)^\tcase (Platform[A-Za-z]+):`).FindAllStringSubmatch(string(src), -1)
	if len(cases) == 0 {
		t.Fatal("no platform cases found - probe regex drift?")
	}
	got := SupportedPlatforms()
	if len(cases) != len(got) {
		t.Fatalf("switch face has %d cases but SupportedPlatforms() lists %d", len(cases), len(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if len(seen) != len(got) {
		t.Fatalf("SupportedPlatforms() has duplicates: %v", got)
	}
}

// TestIssue4986SupportedPlatformsCoversIssuePlatforms asserts every platform
// named by issue #4986 is present.
func TestIssue4986SupportedPlatformsCoversIssuePlatforms(t *testing.T) {
	must := []string{"wecom", "matrix", "signal", "irc", "wechat", "whatsapp", "mattermost", "nostr"}
	seen := map[string]bool{}
	for _, p := range SupportedPlatforms() {
		seen[p] = true
	}
	for _, p := range must {
		if !seen[p] {
			t.Errorf("platform %q missing from SupportedPlatforms()", p)
		}
	}
}
