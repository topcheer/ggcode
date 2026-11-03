package tool

import (
	"net"
	"strings"
	"testing"
)

func TestPrivateNetworksCoversBypassCIDRs(t *testing.T) {
	nets, err := getPrivateNetworks()
	if err != nil {
		t.Fatal(err)
	}
	contains := func(ip net.IP) bool {
		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !contains(net.ParseIP("0.0.0.0")) {
		t.Fatal("0.0.0.0/8 missing from private networks")
	}
	if !contains(net.ParseIP("fd00::1")) {
		t.Fatal("fc00::/7 missing from private networks")
	}
}

func TestWebFetchDescriptionMatchesCap(t *testing.T) {
	if !strings.Contains(WebFetch{}.Description(), "20000") {
		t.Fatal("description does not mention the real 20000-char cap")
	}
}
