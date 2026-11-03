package main

import "testing"

func TestIssue4986IMWhitelistCoversRuntimePlatforms(t *testing.T) {
	rejectedByOldWhitelist := []string{
		"wecom", "matrix", "signal", "irc", "wechat", "whatsapp", "mattermost", "nostr",
	}
	for _, p := range rejectedByOldWhitelist {
		got, err := imValidatePlatform(p)
		if err != nil {
			t.Fatalf("platform %q rejected by CLI whitelist but supported by runtime: %v", p, err)
		}
		if got != p {
			t.Fatalf("platform %q normalized to %q, want identity", p, got)
		}
	}
}

func TestIssue4986IMWhitelistKeepsOriginalSeven(t *testing.T) {
	for _, p := range []string{"qq", "telegram", "feishu", "dingtalk", "discord", "slack", "privateclaw"} {
		got, err := imValidatePlatform(p)
		if err != nil {
			t.Fatalf("original platform %q unexpectedly rejected: %v", p, err)
		}
		if got != p {
			t.Fatalf("platform %q normalized to %q, want identity", p, got)
		}
	}
}

func TestIssue4986IMWhitelistCaseInsensitiveAndUnknownStillRejected(t *testing.T) {
	got, err := imValidatePlatform("WeCom")
	if err != nil || got != "wecom" {
		t.Fatalf("case-insensitive match failed: got %q err %v", got, err)
	}
	if _, err := imValidatePlatform("definitely-not-a-platform"); err == nil {
		t.Fatal("unknown platform unexpectedly accepted")
	}
}
