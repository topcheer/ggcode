package im

import (
	"encoding/binary"
	"net"
	"net/url"
	"testing"
	"time"
)

func fakeSOCKS5Server(t *testing.T, ln net.Listener, chunk int, greeting []byte, reply []byte, proxyRejectMethod bool) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	writeChunked := func(b []byte) {
		for len(b) > 0 {
			n := chunk
			if n <= 0 || n > len(b) {
				n = len(b)
			}
			if _, err := conn.Write(b[:n]); err != nil {
				return
			}
			b = b[n:]
		}
	}

	req := make([]byte, 3)
	if _, err := readFull(conn, req); err != nil {
		return
	}
	if proxyRejectMethod {
		writeChunked([]byte{0x05, 0xFF})
		return
	}
	writeChunked(greeting)

	var connectReq []byte
	buf := make([]byte, 512)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			connectReq = append(connectReq, buf[:n]...)

			if len(connectReq) >= 10 {
				break
			}
		}
		if err != nil {
			return
		}
	}
	writeChunked(reply)

	echo := make([]byte, 64)
	for {
		n, err := conn.Read(echo)
		if n > 0 {
			if _, werr := conn.Write(echo[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func socks5ProxyURL(host string) *url.URL {
	return &url.URL{Scheme: "socks5", Host: host}
}

func TestIssue2970FragmentedGreeting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go fakeSOCKS5Server(t, ln, 1, []byte{0x05, 0x00}, buildReplyDomain("irc.example.com", 6697), false)

	conn, err := socks5Dial(socks5ProxyURL(ln.Addr().String()), "irc.example.com:6697")
	if err != nil {
		t.Fatalf("socks5Dial with 1-byte fragmented greeting failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("PING")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 4)
	if _, err := readFull(conn, got); err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if string(got) != "PING" {
		t.Fatalf("echo = %q, want %q (stream desynchronized)", got, "PING")
	}
}

func TestIssue2970FragmentedReplyDomainAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reply := buildReplyDomain("irc.example.com", 6697)
	go fakeSOCKS5Server(t, ln, 2, []byte{0x05, 0x00}, reply, false)

	conn, err := socks5Dial(socks5ProxyURL(ln.Addr().String()), "irc.example.com:6697")
	if err != nil {
		t.Fatalf("socks5Dial with 2-byte fragmented reply failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("HELLO")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 5)
	if _, err := readFull(conn, got); err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if string(got) != "HELLO" {
		t.Fatalf("echo = %q, want %q (bound addr bytes leaked into stream)", got, "HELLO")
	}
}

func TestIssue2970IPv6BoundAddrDrain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	reply := []byte{0x05, 0x00, 0x00, 0x04}
	reply = append(reply, make([]byte, 16)...)
	reply = binary.BigEndian.AppendUint16(reply, 6697)
	go fakeSOCKS5Server(t, ln, 0, []byte{0x05, 0x00}, reply, false)

	conn, err := socks5Dial(socks5ProxyURL(ln.Addr().String()), "irc.example.com:6697")
	if err != nil {
		t.Fatalf("socks5Dial IPv6 bound addr: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("V6")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 2)
	if _, err := readFull(conn, got); err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if string(got) != "V6" {
		t.Fatalf("echo = %q, want %q", got, "V6")
	}
}

func TestIssue2970AuthRequiredRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go fakeSOCKS5Server(t, ln, 0, nil, nil, true)

	_, err = socks5Dial(socks5ProxyURL(ln.Addr().String()), "irc.example.com:6697")
	if err == nil {
		t.Fatal("socks5Dial with auth-required proxy unexpectedly succeeded")
	}
}

func TestIssue2970UnknownAddressType(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go fakeSOCKS5Server(t, ln, 0, []byte{0x05, 0x00}, []byte{0x05, 0x00, 0x00, 0x09, 0x00, 0x00}, false)

	_, err = socks5Dial(socks5ProxyURL(ln.Addr().String()), "irc.example.com:6697")
	if err == nil {
		t.Fatal("socks5Dial with unknown ATYP 0x09 unexpectedly succeeded")
	}
}

func buildReplyDomain(domain string, port int) []byte {
	reply := []byte{0x05, 0x00, 0x00, 0x03, byte(len(domain))}
	reply = append(reply, domain...)
	return binary.BigEndian.AppendUint16(reply, uint16(port))
}
