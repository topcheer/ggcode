package knight

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	lockPIDOffset = 32
	lockPIDMaxLen = 16
)

func parsePIDBytes(b []byte) int {
	pid, _ := strconv.Atoi(strings.TrimSpace(string(bytes.TrimRight(b, "\x00"))))
	if pid < 0 {
		return 0
	}
	return pid
}

func parseLockFileData(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	if pid := parsePIDBytes(data[:min(len(data), lockPIDMaxLen)]); pid > 0 {
		return pid
	}
	if len(data) > lockPIDOffset {
		if pid := parsePIDBytes(data[lockPIDOffset:min(len(data), lockPIDOffset+lockPIDMaxLen)]); pid > 0 {
			return pid
		}
	}
	return 0
}

func readPIDAt(f *os.File, off int64) int {
	if f == nil {
		return 0
	}
	buf := make([]byte, lockPIDMaxLen)
	n, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return 0
	}
	return parsePIDBytes(buf[:n])
}
