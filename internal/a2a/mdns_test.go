package a2a

import (
	"net"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreferredIP(t *testing.T) {
	ip := PreferredIP()
	if ip == "" {
		t.Fatal("PreferredIP returned empty string")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("PreferredIP returned invalid IP: %q", ip)
	}
	t.Logf("PreferredIP: %s", ip)
}

func TestParseTXTFields(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   map[string]string
	}{
		{
			name:   "standard fields",
			fields: []string{"id=abc", "workspace=/tmp/test", "status=ready", "pid=123"},
			want:   map[string]string{"id": "abc", "workspace": "/tmp/test", "status": "ready", "pid": "123"},
		},
		{
			name:   "empty fields",
			fields: []string{},
			want:   map[string]string{},
		},
		{
			name:   "no equals sign",
			fields: []string{"invalid"},
			want:   map[string]string{},
		},
		{
			name:   "empty value",
			fields: []string{"key="},
			want:   map[string]string{"key": ""},
		},
		{
			name:   "value with equals",
			fields: []string{"url=http://example.com?a=1"},
			want:   map[string]string{"url": "http://example.com?a=1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTXTFields(tt.fields)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestSanitizeMDNSName(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"simple", "simple"},
		{"with space", "with-space"},
		{"with:colon", "with-colon"},
		{"with_underscore", "with-underscore"},
		{"with/path", "with-path"},
		{"mixed :/ _ stuff", "mixed------stuff"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeMDNSName(tt.input)
			if got != tt.output {
				t.Errorf("got %q, want %q", got, tt.output)
			}
		})
	}
}

func TestSanitizeMDNSNameMaxLength(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	result := sanitizeMDNSName(long)
	if len(result) > 63 {
		t.Errorf("result length %d exceeds 63", len(result))
	}
}

func TestSanitizeMDNSNameCJKRuneBoundary(t *testing.T) {
	long := strings.Repeat("好", 100)
	result := sanitizeMDNSName(long)
	if len(result) > 63 {
		t.Errorf("result length %d exceeds 63", len(result))
	}
	if !utf8.ValidString(result) {
		t.Errorf("truncated name is not valid UTF-8: %q", result)
	}
	if n := utf8.RuneCountInString(result); n > 63 {
		t.Errorf("rune count %d exceeds 63", n)
	}
}

func TestMDNSServiceStartStop(t *testing.T) {
	m := newMDNSService()
	if m == nil {
		t.Fatal("newMDNSService returned nil")
	}

	m.stop()

	info := InstanceInfo{
		ID:        "test-id",
		PID:       os.Getpid(),
		Workspace: "/tmp/test-workspace",
		Endpoint:  "127.0.0.1:12345",
		Status:    "ready",
		StartedAt: "2026-04-26T10:00:00Z",
	}
	if err := m.start(info, nil); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	m.stop()

	m.stop()
}

func TestMDNSServiceStartInvalidEndpoint(t *testing.T) {
	m := newMDNSService()
	info := InstanceInfo{
		Endpoint: "invalid-no-port",
	}
	if err := m.start(info, nil); err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

// TestMDNSServiceRestartRetiresOldServer pins #5629: a second start() (the
// restart/session-resume re-Register path) must retire the previous server
// so the stale endpoint TXT stops being announced alongside the new one.
func TestMDNSServiceRestartRetiresOldServer(t *testing.T) {
	m := newMDNSService()
	defer m.stop()

	base := InstanceInfo{
		ID:        "test-id",
		PID:       os.Getpid(),
		Workspace: "/tmp/test-workspace",
		Status:    "ready",
		StartedAt: "2026-04-26T10:00:00Z",
	}
	first := base
	first.Endpoint = "127.0.0.1:12345"
	if err := m.start(first, nil); err != nil {
		t.Fatalf("first start: %v", err)
	}
	oldServer := m.server
	oldBrowser := m.browser
	if oldServer == nil || oldBrowser == nil {
		t.Fatal("first start left nil server/browser")
	}

	second := base
	second.Endpoint = "127.0.0.1:54321"
	if err := m.start(second, nil); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if m.server == oldServer {
		t.Fatal("second start kept the OLD server - stale endpoint keeps announcing (#5629)")
	}
	if m.browser == oldBrowser {
		t.Fatal("second start kept the OLD browser")
	}
	if m.info == nil || m.info.Endpoint != second.Endpoint {
		t.Fatalf("second start did not adopt new endpoint: %+v", m.info)
	}
}

func TestMDNSServiceLookupWithoutStart(t *testing.T) {
	m := newMDNSService()
	instances := m.lookup()
	if instances != nil {
		t.Logf("lookup found %d instances (expected nil without start)", len(instances))
	}
}

func TestMDNSServiceSelfExclusion(t *testing.T) {
	m := newMDNSService()
	info := InstanceInfo{
		ID:        "self-id",
		Endpoint:  "127.0.0.1:19999",
		Workspace: "/tmp/self",
		Status:    "ready",
	}
	if err := m.start(info, nil); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer m.stop()

	for _, inst := range m.lookup() {
		if inst.ID == "self-id" {
			t.Error("lookup should exclude self")
		}
	}
}
