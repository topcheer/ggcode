package lsp

// #3731 probe: readRPCMessage must refuse a Content-Length header beyond
// maxRPCContentLength instead of attempting the allocation - a misbehaving
// third-party server emitting `Content-Length: 4000000000` used to trigger
// a multi-GB make() (or a makeslice panic inside the readLoop goroutine).

import (
	"bufio"
	"strings"
	"testing"
)

func TestIssue3731_ContentLengthCapRejectsHugeHeader(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Content-Length: 4000000000\r\n\r\n"))
	if _, err := readRPCMessage(r); err == nil {
		t.Fatal("huge Content-Length must be rejected as a protocol error")
	}
}

func TestIssue3731_NormalMessageStillReads(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{}}`
	r := bufio.NewReader(strings.NewReader("Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body))
	msg, err := readRPCMessage(r)
	if err != nil {
		t.Fatalf("normal message must still parse: %v", err)
	}
	if string(msg.ID) != "1" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
