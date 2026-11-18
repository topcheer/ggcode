package agent

import (
	"strings"
	"testing"
)

// #5769: lanchat outbound exfiltration-chain alarms must not fire on
// test-fixture placeholder names or word-shaped placeholder values, while
// real .env contents and credential-shaped strings keep alarming.

const issue5769GoFixture = "package agent\n\n" +
	"const testAPIKey = \"test-key-1234567890abcdef\"\n" +
	"const exampleAPIKey = \"example-key-abcdefghijklmnop\"\n" +
	"os.Setenv(\"ISSUE_5759_TOKEN\", \"placeholder-not-real-001\")\n" +
	"TEST_API_KEY=dummy_value_1234567890\n" +
	"FAKE_TOKEN=fake-token-value-12345678\n" +
	"DUMMY_SECRET=dummy_secret_value_000000\n" +
	"MOCK_CLIENT_SECRET=mock-secret-abcdefghij\n"

// Bare generic name followed by a long identifier across a newline: the
// pre-fix regex recorded the next-line identifier as the secret value, which
// then matched VERBATIM in review messages quoting the test names (the
// exact R4-8 false-positive shape).
const issue5769BareNameFixture = "API_KEY=\nISSUE_5759_KEY_PINS_THE_FIX\n"

func TestIssue5769_PlaceholderNamesRecordNoFingerprints(t *testing.T) {
	for name, content := range map[string]string{
		"go-test-fixtures":       issue5769GoFixture,
		"bare-name-newline-word": issue5769BareNameFixture,
	} {
		s := newExfilChainState()
		s.recordSensitiveSource("read_file", "path=/repo/zz_issue5759_test.go", content)
		if len(s.fingerprints) != 0 {
			var got []string
			for _, fp := range s.fingerprints {
				got = append(got, fp.kind+":"+fp.snippet)
			}
			t.Errorf("%s: expected no fingerprints from placeholder content, got %v", name, got)
		}
	}
}

func TestIssue5769_LanchatOutboundPlaceholderMessageNoAlarm(t *testing.T) {
	cases := []struct {
		name    string
		content string
		msg     string
	}{
		{
			"verbatim-placeholder-quote",
			issue5769GoFixture,
			"verified const testAPIKey = \"test-key-1234567890abcdef\" and TEST_API_KEY=dummy_value_1234567890 in zz_issue5759_test.go",
		},
		{
			"name-only-quote",
			issue5769BareNameFixture,
			"checked ISSUE_5759_KEY_PINS_THE_FIX and TEST_API_KEY= names; verdict GO",
		},
		{
			"doc-example-names",
			"EXAMPLE_API_KEY=example-not-a-real-key-0\nFAKE_TOKEN=fake-token-value-12345678\n",
			"docs show EXAMPLE_API_KEY= and FAKE_TOKEN= placeholders only",
		},
	}
	for _, tc := range cases {
		s := newExfilChainState()
		s.recordSensitiveSource("read_file", "path=/repo/zz_issue5759_test.go", tc.content)
		// Immediate outbound send: neither the direct (verbatim) tier nor the
		// proximity tier may fire on placeholder-only fingerprints.
		if g := s.checkExfilChain("lanchat", "action=send message=\""+tc.msg+"\""); g != "" {
			t.Errorf("%s: expected no lanchat chain alarm for placeholder content, got: %s", tc.name, g)
		}
	}
}

func TestIssue5769_RealEnvAndCredentialShapesStillAlarm(t *testing.T) {
	// Non-goal guard: real .env contents keep recording and alarming.
	s := newExfilChainState()
	s.recordSensitiveSource("read_file", "path=/app/.env", exfilTestEnvBody)
	if len(s.fingerprints) == 0 {
		t.Fatal("expected fingerprints from real .env content")
	}
	if g := s.checkExfilChain("lanchat", "action=send message=\"reviewing "+exfilTestSKKey+"\""); g == "" {
		t.Fatal("expected direct chain alarm when a real .env secret is sent via lanchat")
	}

	// Credential-shaped values under generic names must keep recording:
	// mixed case (AbCdEf01...) and specials (sk-ant-...) are not
	// placeholder-shaped; long single-case identifier words are.
	shape := []struct {
		val        string
		credential bool
	}{
		{exfilTestAPIKey, true},
		{exfilTestSKKey, true},
		{"correct-horse-battery-staple", true},
		{"AbCdEf0123456789AbCdEf01", true},
		{"ISSUE_5759_KEY_PINS_THE_FIX", false},
		{"dummy_value_1234567890", false},
		{"testtoken1234", false},
		{"12345678901234567890", false},
	}
	for _, c := range shape {
		if got := exfilPlaceholderShapedValue(c.val); got == c.credential {
			t.Errorf("exfilPlaceholderShapedValue(%q) = %v, want credential=%v", c.val, got, c.credential)
		}
	}
}

func TestIssue5769_RealKeyUnderPlaceholderNameStillAlarms(t *testing.T) {
	// A real ghp_-shaped value under a TEST_-prefixed variable name must
	// still record: value-anchored patterns are immune to name suppression.
	fakeTestFile := "TEST_GITHUB_TOKEN=ghp_" + strings.Repeat("AbCdEfGh", 6) + "\n"
	s := newExfilChainState()
	s.recordSensitiveSource("read_file", "path=/repo/zz_fixture_test.go", fakeTestFile)
	var snippet string
	for _, fp := range s.fingerprints {
		if strings.HasPrefix(fp.snippet, "ghp_") {
			snippet = fp.snippet
		}
	}
	if snippet == "" {
		t.Fatal("expected ghp_-shaped value to be recorded even under a TEST_ variable name")
	}
	if g := s.checkExfilChain("lanchat", "action=send message=\"token: "+snippet+"\""); g == "" {
		t.Fatal("expected chain alarm when a real-shaped key is sent via lanchat")
	}
}

func TestIssue5769_PlaceholderNameClassifier(t *testing.T) {
	placeholders := []string{
		"TEST_API_KEY", "testAPIKey", "Issue_5759_KEY", "ISSUE-5759-API-KEY",
		"EXAMPLE_TOKEN", "exampleApiKey", "FAKE_SECRET", "fakeToken",
		"DUMMY_PASSWORD", "dummyClientSecret", "SAMPLE_API_KEY", "MOCK_TOKEN",
		"STUB_SECRET", "PLACEHOLDER_API_KEY", "FIXTURE_TOKEN",
	}
	for _, id := range placeholders {
		if !exfilPlaceholderName(id) {
			t.Errorf("exfilPlaceholderName(%q) = false, want true", id)
		}
	}
	real := []string{
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY",
		"DATABASE_URL", "ZAI_API_KEY", "CONTEST_TOKEN", "LATEST_KEY", "PROTEST_SECRET",
		"", "api_key",
	}
	for _, id := range real {
		if exfilPlaceholderName(id) {
			t.Errorf("exfilPlaceholderName(%q) = true, want false", id)
		}
	}
}
