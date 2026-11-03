package main

import (
	"os"
	"strings"
	"testing"
)

func sliceFunc(t *testing.T, src, startMarker, endMarker string) string {
	t.Helper()
	i := strings.Index(src, startMarker)
	if i < 0 {
		t.Fatalf("marker %q not found", startMarker)
	}
	rest := src[i:]
	j := strings.Index(rest[len(startMarker):], endMarker)
	if j < 0 {
		t.Fatalf("end marker %q not found after %q", endMarker, startMarker)
	}
	return rest[:len(startMarker)+j]
}

func TestIssue2417AddEntryValidatesPlatform(t *testing.T) {
	b, err := os.ReadFile("im_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	block := sliceFunc(t, src, "--platform is required", "loadIMConfig")
	if !strings.Contains(block, "imValidatePlatform") {
		t.Fatal("#2417: `im config add` must validate/canonicalize the platform before persisting")
	}
}

func TestIssue2417SetPlatformValidates(t *testing.T) {
	b, err := os.ReadFile("im_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	block := sliceFunc(t, src, `case "platform":`, "PatchIMAdapter")
	if !strings.Contains(block, "imValidatePlatform") {
		t.Fatal("#2417: `im config set <name> platform` must validate/canonicalize the value before persisting")
	}
	if !strings.Contains(block, "adapter.Platform = value") {
		t.Fatal("#2417: set-platform must still persist the (canonicalized) value")
	}
}

func TestIssue2417WizardRawInputValidates(t *testing.T) {
	b, err := os.ReadFile("im_cmd.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	block := sliceFunc(t, src, "platformMap[platformChoice]", "Step 3")
	if !strings.Contains(block, "imValidatePlatform") {
		t.Fatal("#2417: wizard raw-input branch must validate the platform instead of passing typos through")
	}

	if strings.Contains(block, "allow raw input") {
		t.Fatal("#2417: wizard must not blindly `plt = platformChoice // allow raw input`")
	}
}

func TestIssue2417CanonicalizerSemantics(t *testing.T) {

	cases := map[string]string{
		"qq":       "qq",
		"Telegram": "telegram",
		"TELEGARM": "",
		"telegarm": "",
		"slack":    "slack",
		"SlAcK":    "slack",
		"nope":     "",
	}
	for in, want := range cases {
		got, err := imValidatePlatform(in)
		if want == "" {
			if err == nil {
				t.Errorf("imValidatePlatform(%q) = %q, want rejection", in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("imValidatePlatform(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("imValidatePlatform(%q) = %q, want %q", in, got, want)
		}
	}
}
