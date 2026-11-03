package tunnel

import "testing"

func TestIsLocalRelayHostExplicitOnly(t *testing.T) {
	local := []string{"localhost", "host.docker.internal", "127.0.0.1", "192.168.1.10", "10.0.0.5", "172.16.0.1", "::1", "fd00::1"}
	for _, h := range local {
		if !isLocalRelayHost(h) {
			t.Errorf("%q should be local", h)
		}
	}

	public := []string{"ai", "relay", "foo.local", "example.com", "8.8.8.8", "1.1.1.1"}
	for _, h := range public {
		if isLocalRelayHost(h) {
			t.Errorf("%q must NOT be treated as local (#1400)", h)
		}
	}
}
