//go:build linux

package tool

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// spawnPressure returns the #4852 Ask-#1 saturation metrics without
// spawning any process (a failing-fork host may also fail probe spawns).
func spawnPressure() string {
	var parts []string

	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) >= 3 {
			parts = append(parts, "loadavg="+f[0]+" "+f[1]+" "+f[2])
		}
	}
	if raw, err := os.ReadFile("/proc/sys/kernel/pid_max"); err == nil {
		if v, perr := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); perr == nil {
			parts = append(parts, fmt.Sprintf("pid_max=%d", v))
		}
	}

	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		parts = append(parts, fmt.Sprintf("self_fd_limit=%d", limit.Cur))
	}
	if fds, err := os.ReadDir("/proc/self/fd"); err == nil {
		parts = append(parts, fmt.Sprintf("self_fds=%d", len(fds)))
	}
	if entries, err := os.ReadDir("/proc"); err == nil {
		procs := 0
		for _, e := range entries {
			if _, perr := strconv.Atoi(e.Name()); perr == nil {
				procs++
			}
		}
		parts = append(parts, fmt.Sprintf("procs=%d", procs))
	}

	if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			name := strings.TrimSuffix(fields[0], ":")
			if name == "MemTotal" || name == "MemAvailable" {
				unit := "kB"
				if len(fields) >= 3 {
					unit = fields[2]
				}
				parts = append(parts, name+"="+fields[1]+unit)
			}
		}
	}

	if raw, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		if line := firstLine(string(raw)); line != "" {
			parts = append(parts, "psi_mem="+strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "some")))
		}
	} else if raw, err := os.ReadFile("/proc/vmstat"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "pg_scan ") {
				parts = append(parts, "vmstat_pg_scan="+strings.TrimSpace(strings.TrimPrefix(line, "pg_scan ")))
				break
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return "[S-16 pressure] " + strings.Join(parts, " ")
}
