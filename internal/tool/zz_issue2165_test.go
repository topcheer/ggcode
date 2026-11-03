package tool

import (
	"context"
	"net"
	"testing"
)

func TestRejectIfHostResolvesPrivate(t *testing.T) {

	err := rejectIfHostResolvesPrivate(context.Background(), "x.corp.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
		})
	if err == nil {
		t.Fatal("private A record must be rejected on the proxy path")
	}

	err = rejectIfHostResolvesPrivate(context.Background(), "mixed.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("192.168.0.1")}}, nil
		})
	if err == nil {
		t.Fatal("any private IP in the record set must reject")
	}

	err = rejectIfHostResolvesPrivate(context.Background(), "ok.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		})
	if err != nil {
		t.Fatalf("public A record must pass: %v", err)
	}

	err = rejectIfHostResolvesPrivate(context.Background(), "unresolvable.example.com",
		func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{Err: "no such host"}
		})
	if err != nil {
		t.Fatalf("lookup failure must not reject: %v", err)
	}

	err = rejectIfHostResolvesPrivate(context.Background(), "10.0.0.1", nil)
	if err != nil {
		t.Fatalf("literal IP must be deferred to isPrivateHost: %v", err)
	}
}
