package mcp

import (
	"context"
	"time"
)

// #5814 liveness lease: the client remembers the last time a request completed
// a full round-trip (response received, even a JSON-RPC error response - the
// wire itself working is the liveness evidence). Long-lived instances use this
// to fast-fail calls against half-dead stdio servers (process alive, pipe
// stalled) instead of hanging to the request timeout.

// markHealthy records a successful round-trip. Called on every completed
// request and every successful probe.
func (c *Client) markHealthy() {
	c.lastHealthyAt.Store(time.Now().UnixNano())
}

// LastHealthyAt returns the time of the last completed round-trip, or the zero
// time when no round-trip ever succeeded on this client.
func (c *Client) LastHealthyAt() time.Time {
	ns := c.lastHealthyAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// SetLivenessLease sets how old LastHealthyAt may be before pre-call checks
// consider the server likely dead. maxAge <= 0 disables the lease.
func (c *Client) SetLivenessLease(maxAge time.Duration) {
	c.leaseMaxAge.Store(int64(maxAge))
}

// LivenessLeaseState reports the lease inputs so callers can build their own
// diagnostics. expired is only true when a lease is configured, a round-trip
// has succeeded at least once, and that evidence is older than the lease.
func (c *Client) LivenessLeaseState() (last time.Time, maxAge time.Duration, expired bool) {
	last = c.LastHealthyAt()
	maxAge = time.Duration(c.leaseMaxAge.Load())
	if maxAge <= 0 || last.IsZero() {
		return last, maxAge, false
	}
	return last, maxAge, time.Since(last) > maxAge
}

const (
	probePrefUnknown = int32(0)
	probePrefPing    = int32(1)
	probePrefTools   = int32(2)
)

// Probe performs a lightweight liveness request against the server: the
// capability-negotiated ping request when the server answers it, otherwise a
// count-only tools/list call. Any response - including a JSON-RPC error such
// as method-not-found - proves the transport is alive; only transport-level
// failures (timeouts, broken pipes, closed connections) are reported as
// errors. A transport failure on the preferred probe method is taken at face
// value: a stalled pipe would stall the fallback method too, so no cross-check
// is attempted (half-dead servers must be detected on the first probe, #5814).
// A successful probe refreshes the liveness lease.
func (c *Client) Probe(ctx context.Context) error {
	pref := c.probePref.Load()
	if pref != probePrefTools {
		err := c.sendRequest(ctx, "ping", struct{}{}, nil)
		if err == nil {
			c.probePref.Store(probePrefPing)
			c.markHealthy()
			return nil
		}
		if _, rpcErr := jsonRPCErrorOf(err); rpcErr {
			// The server responded with a JSON-RPC error (ping unsupported
			// here): the round-trip itself is the liveness evidence. Remember
			// the tools/list fallback for future probes.
			c.probePref.Store(probePrefTools)
			c.markHealthy()
			return nil
		}
		return err
	}
	err := c.sendRequest(ctx, "tools/list", ListToolsParams{}, nil)
	if err == nil {
		c.markHealthy()
		return nil
	}
	if _, rpcErr := jsonRPCErrorOf(err); rpcErr {
		c.markHealthy()
		return nil
	}
	return err
}
