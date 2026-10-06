package knight

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)

// Lock-file PID layout conventions per platform (#3020):
//
//	unix:    PID written at offset 0 (flock is advisory; whole-file reads work)
//	windows: PID written at offset 32 (LockFileEx byte-0 mandatory lock makes
//	         offset-0 unreadable through other handles while held)
//
// The same project tree can be visited by instances on different platforms
// (WSL <-> Windows native, macOS <-> SMB share, CI checkout reuse), sharing
// one .ggcode/knight.lock. Reading with only the local convention yields
// PID=0 on cross-platform files, degrading "held by PID X" diagnostics to a
// bare "stopped". The parse below understands both layouts: local first,
// then the other platform's offset.
const (
	lockPIDOffset = 32
	lockPIDMaxLen = 16
)

// parsePIDBytes parses a PID from raw bytes, tolerating zero padding and
// surrounding whitespace. Returns 0 when the bytes are not a PID.
func parsePIDBytes(b []byte) int {
	pid, _ := strconv.Atoi(strings.TrimSpace(string(bytes.TrimRight(b, "\x00"))))
	if pid < 0 {
		return 0
	}
	return pid
}

// parseLockFileData extracts the holder PID from lock-file content using
// either platform's layout: unix offset-0 first, then windows offset-32.
func parseLockFileData(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	// Unix layout: plain PID text at offset 0. On a windows-written file
	// the first bytes are zero padding, which fails to parse and falls
	// through to the offset-32 read below.
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

// readPIDAt reads up to lockPIDMaxLen bytes at off and parses a PID.
// Returns 0 on read error or unparsable content.
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
