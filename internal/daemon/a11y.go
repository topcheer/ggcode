package daemon

import (
	"os"
)

func colorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func init() {
	if colorEnabled() {
		return
	}

	ansiDim, ansiReset = "", ""
	ansiBold, ansiFgYellow = "", ""
	ansiFgGreen, ansiFgRed = "", ""
	ansiBgBlue = ""
	ansiClearLine = "\r"
}
