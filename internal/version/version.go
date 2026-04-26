package version

import "strings"

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func Display() string {
	if v := strings.TrimSpace(Version); v != "" {
		return v
	}
	return "dev"
}

func IsDev() bool {
	v := strings.TrimSpace(strings.ToLower(Display()))
	return v == "" || v == "dev"
}
