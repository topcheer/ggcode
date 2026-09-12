package tool

// #2165 regression: with a proxy configured, the DialContext-level SSRF
// guard never ran (the proxy resolves the hostname itself) and the
// "URL-level" check was literal-only - a domain whose A record pointed
// at a private IP went straight through to the proxy.

import (
	"context"
	"net"
	"testing"
)

func TestRejectIfHostResolvesPrivate(t *testing.T) {
	// Private A record: rejected.
	err := rejectIfHostResolvesPrivate(context.Background(), "x.corp.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
		})
	if err == nil {
		t.Fatal("private A record must be rejected on the proxy path")
	}
	// Mixed records: ANY private IP rejects.
	err = rejectIfHostResolvesPrivate(context.Background(), "mixed.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("192.168.0.1")}}, nil
		})
	if err == nil {
		t.Fatal("any private IP in the record set must reject")
	}
	// Public only: allowed.
	err = rejectIfHostResolvesPrivate(context.Background(), "ok.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		})
	if err != nil {
		t.Fatalf("public A record must pass: %v", err)
	}
	// Lookup failure: NOT rejected (proxy may resolve differently).
	err = rejectIfHostResolvesPrivate(context.Background(), "unresolvable.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{Err: "no such host"}
		})
	if err != nil {
		t.Fatalf("lookup failure must not reject: %v", err)
	}
	// Literal IPs are the literal guard's job.
	err = rejectIfHostResolvesPrivate(context.Background(), "10.0.0.1", nil)
	if err != nil {
		t.Fatalf("literal IP must be deferred to isPrivateHost: %v", err)
	}
}
