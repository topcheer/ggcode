package image

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadLimited(t *testing.T) {

	small := bytes.Repeat([]byte("x"), 100)
	got, err := ReadLimited(bytes.NewReader(small), 1024)
	if err != nil || !bytes.Equal(got, small) {
		t.Fatalf("under-limit read failed: err=%v len=%d", err, len(got))
	}

	exact := bytes.Repeat([]byte("y"), 64)
	got, err = ReadLimited(bytes.NewReader(exact), 64)
	if err != nil || !bytes.Equal(got, exact) {
		t.Fatalf("at-limit read failed: err=%v len=%d", err, len(got))
	}

	big := bytes.Repeat([]byte("z"), MaxSize+1)
	_, err = ReadLimited(bytes.NewReader(big), MaxSize)
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected size-limit error, got %v", err)
	}

	_, err = ReadLimited(errReader{}, 10)
	if !errors.Is(err, errFail) {
		t.Fatalf("expected reader error, got %v", err)
	}
}

type errFailErr struct{}

func (errFailErr) Error() string { return "boom" }

var errFail error = errFailErr{}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errFail }
