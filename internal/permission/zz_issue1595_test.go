package permission

import (
	"testing"
)

func TestIssue1595_DdWriteToDevice(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"dd of=/dev/sda if=disk.img bs=4M", true},
		{"dd of=/dev/disk2 if=img.raw", true},
		{"dd of=/dev/nvme0n1 if=img.raw", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"dd if=data.txt of=out.img", false},
	}
	for _, c := range cases {
		got := NewDangerousDetector().Check(c.cmd)
		if (got.Level == DangerCritical) != c.want {
			t.Errorf("%q: Critical=%v, want %v", c.cmd, got.Level == DangerCritical, c.want)
		}
	}
}

func TestIssue1595_UrlInApprovalKey(t *testing.T) {
	k1, ok := MakeKey("web_fetch", []byte(`{"url":"https://a.example.com/docs/x?y=1"}`))
	if !ok || k1 == "web_fetch" {
		t.Fatalf("url must enter the key, got %q (ok=%v)", k1, ok)
	}
	k2, _ := MakeKey("web_fetch", []byte(`{"url":"https://b.example.com/other"}`))
	if k1 == k2 {
		t.Fatalf("different sites must produce different keys: %q == %q", k1, k2)
	}
	k3, _ := MakeKey("web_fetch", []byte(`{"url":"https://a.example.com/docs/deeper/page"}`))
	if k1 != k3 {
		t.Fatalf("same site (scheme+host+first segment) must share the key: %q vs %q", k1, k3)
	}
}
