package a2a

import (
	"net"
	"os"
	"testing"

	"github.com/hashicorp/mdns"
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
		name     string
		fields   []string
		expected map[string]string
	}{
		{
			name:   "standard fields",
			fields: []string{"id=abc123", "workspace=/ws/orders", "status=ready", "pid=12345"},
			expected: map[string]string{
				"id":        "abc123",
				"workspace": "/ws/orders",
				"status":    "ready",
				"pid":       "12345",
			},
		},
		{
			name:     "empty fields",
			fields:   []string{},
			expected: map[string]string{},
		},
		{
			name:   "value with equals",
			fields: []string{"key=val=ue"},
			expected: map[string]string{
				"key": "val=ue",
			},
		},
		{
			name:   "empty value",
			fields: []string{"key="},
			expected: map[string]string{
				"key": "",
			},
		},
		{
			name:     "no equals",
			fields:   []string{"invalid"},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTXTFields(tt.fields)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d fields, got %d", len(tt.expected), len(got))
			}
			for k, v := range tt.expected {
				if got[k] != v {
					t.Errorf("key %q: expected %q, got %q", k, v, got[k])
				}
			}
		})
	}
}

// --- sanitizeMDNSName ---

func TestSanitizeMDNSName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"order-service:12345", "order-service-12345"},
		{"my project", "my-project"},
		{"under_score", "under-score"},
		{"already-clean", "already-clean"},
		{"path/to/project", "path-to-project"},
		{"", ""},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := sanitizeMDNSName(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeMDNSName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeMDNSNameMaxLength(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := sanitizeMDNSName(long)
	if len(got) > 63 {
		t.Errorf("result length %d exceeds 63", len(got))
	}
	if len(got) != 63 {
		t.Errorf("expected truncation to 63, got %d", len(got))
	}
}

// --- entryToInstance ---

func TestEntryToInstance(t *testing.T) {
	tests := []struct {
		name     string
		entry    *mdns.ServiceEntry
		expected *InstanceInfo
		nil      bool
	}{
		{
			name: "full entry",
			entry: &mdns.ServiceEntry{
				Name:       "order-service-12345",
				Host:       "macbook.local.",
				AddrV4:     net.ParseIP("192.168.1.10"),
				Port:       12345,
				InfoFields: []string{"id=ggcode-mac-12345-678", "workspace=/Users/dev/orders", "status=ready", "pid=12345", "started=2026-04-26T10:00:00Z"},
			},
			expected: &InstanceInfo{
				ID:           "ggcode-mac-12345-678",
				PID:          12345,
				Workspace:    "/Users/dev/orders",
				StartedAt:    "2026-04-26T10:00:00Z",
				Endpoint:     "192.168.1.10:12345",
				AgentCardURL: "192.168.1.10:12345/.well-known/agent.json",
				Status:       "ready",
			},
		},
		{
			name: "minimal entry",
			entry: &mdns.ServiceEntry{
				Name:   "test",
				AddrV4: net.ParseIP("10.0.0.1"),
				Port:   8080,
			},
			expected: &InstanceInfo{
				ID:           "mdns-test-8080",
				Workspace:    "test",
				Endpoint:     "10.0.0.1:8080",
				AgentCardURL: "10.0.0.1:8080/.well-known/agent.json",
			},
		},
		{
			name:  "nil entry",
			entry: nil,
			nil:   true,
		},
		{
			name: "no IP",
			entry: &mdns.ServiceEntry{
				Name: "no-ip",
				Port: 8080,
			},
			nil: true,
		},
		{
			name: "fallback to deprecated Addr field",
			entry: &mdns.ServiceEntry{
				Name: "fallback",
				Addr: net.ParseIP("172.16.0.1"),
				Port: 9090,
			},
			expected: &InstanceInfo{
				ID:           "mdns-fallback-9090",
				Workspace:    "fallback",
				Endpoint:     "172.16.0.1:9090",
				AgentCardURL: "172.16.0.1:9090/.well-known/agent.json",
			},
		},
		{
			name: "IPv6 preferred over nothing",
			entry: &mdns.ServiceEntry{
				Name: "ipv6",
				Addr: net.ParseIP("::1"),
				Port: 7070,
			},
			expected: &InstanceInfo{
				ID:           "mdns-ipv6-7070",
				Workspace:    "ipv6",
				Endpoint:     "::1:7070",
				AgentCardURL: "::1:7070/.well-known/agent.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entryToInstance(tt.entry)
			if tt.nil {
				if got != nil {
					t.Fatal("expected nil")
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.ID != tt.expected.ID {
				t.Errorf("ID: got %q, want %q", got.ID, tt.expected.ID)
			}
			if got.Workspace != tt.expected.Workspace {
				t.Errorf("Workspace: got %q, want %q", got.Workspace, tt.expected.Workspace)
			}
			if got.Endpoint != tt.expected.Endpoint {
				t.Errorf("Endpoint: got %q, want %q", got.Endpoint, tt.expected.Endpoint)
			}
			if got.AgentCardURL != tt.expected.AgentCardURL {
				t.Errorf("AgentCardURL: got %q, want %q", got.AgentCardURL, tt.expected.AgentCardURL)
			}
			if got.Status != tt.expected.Status {
				t.Errorf("Status: got %q, want %q", got.Status, tt.expected.Status)
			}
			if got.PID != tt.expected.PID {
				t.Errorf("PID: got %d, want %d", got.PID, tt.expected.PID)
			}
			if got.StartedAt != tt.expected.StartedAt {
				t.Errorf("StartedAt: got %q, want %q", got.StartedAt, tt.expected.StartedAt)
			}
		})
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
	// lookup without start should return nil (no self to exclude)
	instances := m.lookup()
	// May or may not find other instances on the network, but should not panic.
	t.Logf("lookup found %d instances", len(instances))
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

func TestPreferredInterface(t *testing.T) {
	iface := PreferredInterface()
	if iface == nil {
		t.Fatal("PreferredInterface returned nil")
	}
	t.Logf("Interface: %s (index %d, flags=%x)", iface.Name, iface.Index, iface.Flags)

	ip := PreferredIP()
	t.Logf("PreferredIP: %s", ip)

	// Verify the interface actually has this IP
	addrs, err := iface.Addrs()
	if err != nil {
		t.Fatalf("Addrs: %v", err)
	}
	found := false
	for _, addr := range addrs {
		var ifaceIP net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ifaceIP = v.IP
		}
		if ifaceIP != nil && ifaceIP.String() == ip {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("IP %s not found on interface %s addrs: %v", ip, iface.Name, addrs)
	}
}
