package daemon

// itoaDaemon converts a pid to its decimal string form without pulling in
// strconv for this tiny hot path (kept platform-neutral so every
// processCmdline variant can share it).
func itoaDaemon(pid int) string {
	if pid == 0 {
		return "0"
	}
	neg := pid < 0
	if neg {
		pid = -pid
	}
	var buf [20]byte
	i := len(buf)
	for pid > 0 {
		i--
		buf[i] = byte('0' + pid%10)
		pid /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
