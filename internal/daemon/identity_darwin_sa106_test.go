//go:build darwin

package daemon

import "testing"

func TestTrimProcargsToArgvTableSa106(t *testing.T) {
	// Layout: [4-byte LE argc][argv NUL-terminated strings][padding][env].
	mk := func(argc int32, argv []string, env string) string {
		b := []byte{byte(argc), byte(argc >> 8), byte(argc >> 16), byte(argc >> 24)}
		for _, a := range argv {
			b = append(b, a...)
			b = append(b, 0)
		}
		b = append(b, 0, 0) // padding
		b = append(b, env...)
		return string(b)
	}

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "normal argv with env region",
			raw:  mk(3, []string{"ggcode", "daemon", "--__daemonized"}, "SECRET=1\x00PATH=/bin\x00"),
			want: "ggcode daemon --__daemonized",
		},
		{
			name: "env containing daemon marker must be excluded (#431)",
			raw:  mk(1, []string{"/bin/sh"}, "DAEMON_ARGS=--__daemonized\x00"),
			want: "/bin/sh",
		},
		{
			name: "too short",
			raw:  "abc",
			want: "",
		},
		{
			name: "zero argc invalid",
			raw:  mk(0, nil, "env"),
			want: "",
		},
		{
			name: "argc above cap invalid",
			raw:  mk(4097, nil, "env"),
			want: "",
		},
		{
			name: "malformed missing NUL terminator",
			raw:  "\x02\x00\x00\x00unterminated",
			want: "",
		},
	}

	for _, c := range cases {
		if got := trimProcargsToArgv(c.raw); got != c.want {
			t.Errorf("%s: trimProcargsToArgv = %q, want %q", c.name, got, c.want)
		}
	}
}
