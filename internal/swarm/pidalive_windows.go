//go:build windows

package swarm

// pidAlive: Windows has no signal-0 semantics; owner-lease liveness falls
// back to the heartbeat TTL (see ownerLease.alive).
func pidAlive(pid int) bool {
	return false
}
