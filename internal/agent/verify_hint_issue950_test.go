package agent

import "testing"

func TestIsVerifyCommandEnvPrefixAndCompound(t *testing.T) {
	positive := []string{

		`GOFLAGS="-p=1" make verify-ci`,
		`GOFLAGS=-p=1 make verify-ci`,
		`CGO_ENABLED=0 go build ./...`,
		`GOMEMLIMIT=2GiB GOGC=50 go test ./...`,

		`cd /app && go test ./...`,
		`git pull && make test`,
		`echo hi; go vet ./...`,
		`grep foo bar || make test`,

		`make verify-ci`,
		`go test ./...`,
		`task build`,
	}
	for _, cmd := range positive {
		if !isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = false, want true", cmd)
		}
	}
	negative := []string{
		`ls -la`,
		`cat README.md`,
		`echo hello world`,
		`cd /app && ls`,
		``,
		`   `,
	}
	for _, cmd := range negative {
		if isVerifyCommand(cmd) {
			t.Errorf("isVerifyCommand(%q) = true, want false", cmd)
		}
	}
}

func TestStripEnvAssignments(t *testing.T) {
	cases := []struct{ in, want string }{
		{`GOFLAGS="-p=1" make verify-ci`, `make verify-ci`},
		{`CGO_ENABLED=0 go build ./...`, `go build ./...`},
		{`A=1 B=2 C=3 go test`, `go test`},
		{`make test`, `make test`},
		{`echo a=b`, `echo a=b`},
	}
	for _, c := range cases {
		if got := stripEnvAssignments(c.in); got != c.want {
			t.Errorf("stripEnvAssignments(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
