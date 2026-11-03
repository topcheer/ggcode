package tool

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// spawnPressureDarwin returns the #4852 Ask-#1 saturation metrics without
// spawning any process (a failing-fork host may also fail probe spawns).
func spawnPressure() string {
	var parts []string

	if raw, err := unix.SysctlRaw("kern.proc.all"); err == nil {
		if sz := int(unsafe.Sizeof(unix.KinfoProc{})); sz > 0 {
			parts = append(parts, fmt.Sprintf("procs=%d", len(raw)/sz))
		}
	}
	if v, err := unix.SysctlUint32("kern.maxproc"); err == nil && v > 0 {
		parts = append(parts, fmt.Sprintf("maxproc=%d", v))
	}

	if fds, err := os.ReadDir("/dev/fd"); err == nil {
		parts = append(parts, fmt.Sprintf("self_fds=%d", len(fds)))
	}
	if v, err := unix.SysctlUint32("kern.num_files"); err == nil && v > 0 {
		parts = append(parts, fmt.Sprintf("sys_open_files=%d", v))
	}
	if v, err := unix.SysctlUint32("kern.maxfiles"); err == nil && v > 0 {
		parts = append(parts, fmt.Sprintf("sys_max_files=%d", v))
	}

	// vm.loadavg returns struct loadavg: 3×uint32 fixpoint loads followed
	// by an int64 Fscale. Decode from the raw sysctl; scale-by-65536 and
	// similar guesses disagree with `sysctl -n vm.loadavg` by 25x.
	if raw, err := unix.SysctlRaw("vm.loadavg"); err == nil && len(raw) >= 24 {
		if fscale := binary.LittleEndian.Uint64(raw[16:24]); fscale > 0 {
			ld := func(i int) float64 {
				return float64(binary.LittleEndian.Uint32(raw[i*4:(i+1)*4])) / float64(fscale)
			}
			parts = append(parts, fmt.Sprintf("loadavg=%.2f %.2f %.2f", ld(0), ld(1), ld(2)))
		}
	}
	if v, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		parts = append(parts, fmt.Sprintf("vm_pressure=%d", v))
	}
	if v, err := unix.SysctlUint32("hw.ncpu"); err == nil && v > 0 {
		parts = append(parts, fmt.Sprintf("ncpu=%d", v))
	}

	if len(parts) == 0 {
		return ""
	}
	return "[S-16 pressure] " + strings.Join(parts, " ")
}
