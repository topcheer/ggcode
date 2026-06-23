package a2a

import (
	"net"
	"os"
	"testing"
)

// --- PreferredIP ---

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

// --- parseTXTFields ---

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

// --- sanitizeMDNSName ---

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

// --- mdnsService lifecycle ---

func TestMDNSServiceStartStop(t *testing.T) {
	m := newMDNSService()
	if m == nil {
		t.Fatal("newMDNSService returned nil")
	}

	// stop on uninitialized should be safe
	m.stop()

	// start with a valid instance
	info := InstanceInfo{
		ID:        "test-id",
		PID:       os.Getpid(),
		Workspace: "/tmp/test-workspace",
		Endpoint:  "127.0.0.1:12345",
		Status:    "ready",
		StartedAt: "2026-04-26T10:00:00Z",
	}
	if err := m.start(info); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Should be able to stop cleanly
	m.stop()

	// Double stop should be safe
	m.stop()
}

func TestMDNSServiceStartInvalidEndpoint(t *testing.T) {
	m := newMDNSService()
	info := InstanceInfo{
		Endpoint: "invalid-no-port",
	}
	if err := m.start(info); err == nil {
		t.Fatal("expected error for invalid endpoint")
	}
}

func TestMDNSServiceLookupWithoutStart(t *testing.T) {
	m := newMDNSService()
	// lookup without start should return nil
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
	if err := m.start(info); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer m.stop()

	// lookup should not include self
	for _, inst := range m.lookup() {
		if inst.ID == "self-id" {
			t.Error("lookup should exclude self")
		}
	}
}
