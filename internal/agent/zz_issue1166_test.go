package agent

import "testing"

const issue1166CommaOkSrc = `package sample

type Config struct{ Host string }

func FromMap(m map[string]Config, name string) string {
	v, e := m[name]
	if !e {
		return ""
	}
	return v.Host
}

func FromAssert(x interface{}) string {
	v, e := x.(Config)
	_ = e
	return v.Host
}

func FromChannel(ch chan Config) string {
	v, e := <-ch
	_ = e
	return v.Host
}
`

const issue1166ErrorReturnSrc = `package sample

type Config struct{ Host string }

func load() (Config, error) {
	return Config{}, nil
}

func RealBug() string {
	v, err := load()
	return v.Host
}
`

func TestIssue1166_CommaOkAssignmentsNotFlagged(t *testing.T) {
	if got := checkNilDerefAfterError("sample.go", "", issue1166CommaOkSrc); got != "" {
		t.Fatalf("comma-ok forms flagged as error-return, got:\n%s", got)
	}
}

func TestIssue1166_ErrorReturnStillFlagged(t *testing.T) {
	got := checkNilDerefAfterError("sample.go", "", issue1166ErrorReturnSrc)
	if got == "" {
		t.Fatal("expected a nil-deref-after-error warning for a true error-return assignment")
	}
}

func TestIssue1166_CommaOkDeltaSuppressed(t *testing.T) {
	oldSrc := issue1166CommaOkSrc + "\nfunc extra() {}\n"
	if got := checkNilDerefAfterError("sample.go", oldSrc, issue1166CommaOkSrc); got != "" {
		t.Fatalf("comma-ok forms flagged in delta mode, got:\n%s", got)
	}
}
