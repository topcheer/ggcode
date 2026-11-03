//go:build unix

package runfile

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)

	return err == nil || err == syscall.EPERM
}

func procStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}

	i := bytes.LastIndexByte(data, ')')
	if i < 0 || i+2 > len(data) {
		return ""
	}
	fields := strings.Fields(string(data[i+2:]))

	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
