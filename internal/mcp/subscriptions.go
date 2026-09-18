package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Modern subscription streams — "subscriptions/listen" (MCP protocol
// revision 2026-07-28, notifications/subscriptions/acknowledged).
//
// The initialize-era push model (standalone GET SSE stream on streamable
// HTTP; plain stdio writes) carries no correlation metadata: a client cannot
// prove a notification is addressed to it, and the server cannot scope what
// it pushes. The 2026-07-28 revision replaces this with an explicit
// request/response stream:
//
//	→ {"method": "subscriptions/listen",
//	   "params": {"notifications": {"toolsListChanged": true, ...}}, "id": 1}
//	← notifications/subscriptions/acknowledged {params._meta[subId], params.notifications}
//	← ... correlated notifications, each params._meta carrying the
//	    subscriptionId ...
//	← {"result": {...}, "id": 1}          ← graceful stream closure
//
// Server-side MUSTs we implement:
//   - the server MUST NOT deliver notifications before acknowledging, so any
//     notification whose subscriptionId is unknown or not yet acknowledged is
//     a protocol violation → dropped with a debug log;
//   - every listen-stream notification carries params._meta[".../subscriptionId"]
//     → used for correlation here, then stripped from the user's view (the
//     legacy handler keeps receiving the plain method/params it always did);
//   - notifications/subscriptions/acknowledged is a control message and is
//     consumed here, never forwarded.
//
// Client-side MUST we implement: when closing a subscription before the
// server does, send notifications/cancelled (reuses the existing
// Client.notifyCancelled plumbing).
//
// Transport notes: stdio delivers the eventual response through the shared
// read loop's waiter registry; streamable HTTP delivers it as the final SSE
// event of the long-lived POST (interleaved notifications ride the same
// stream into processNotification). WebSocket is intentionally unsupported
// for listen (legacy ws bridge, no ggcode-managed server uses it).

const (
	// MethodSubscriptionsListen requests a correlated notification stream.
	MethodSubscriptionsListen = "subscriptions/listen"
	// NotificationSubscriptionsAcknowledged confirms the negotiated filter.
	NotificationSubscriptionsAcknowledged = "notifications/subscriptions/acknowledged"
	// MetaKeySubscriptionID is the params._meta key carrying the JSON-RPC id
	// of the subscriptions/listen request a notification belongs to.
	MetaKeySubscriptionID = "io.modelcontextprotocol/subscriptionId"

	// mcpSubscriptionAckTimeout bounds the wait for the acknowledged
	// notification. A compliant server acks before delivering anything, so a
	// healthy roundtrip is milliseconds; anything slower is treated as a
	// failed subscription and cancelled.
	mcpSubscriptionAckTimeout = 15 * time.Second
)

// errCodeMethodNotFound is the JSON-RPC error a legacy server returns for an
// unknown method; used to downgrade to the legacy push path permanently.
const errCodeMethodNotFound = -32601

// SubscriptionFilter describes which notification types a client requests on
// a subscriptions/listen stream (params.notifications).
type SubscriptionFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged,omitempty"`
	PromptsListChanged    bool     `json:"promptsListChanged,omitempty"`
	ResourcesListChanged  bool     `json:"resourcesListChanged,omitempty"`
	ResourceSubscriptions []string `json:"resourceSubscriptions,omitempty"`
}

func (f SubscriptionFilter) empty() bool {
	return !f.ToolsListChanged && !f.PromptsListChanged && !f.ResourcesListChanged &&
		len(f.ResourceSubscriptions) == 0
}

// missingFrom returns the human-readable entries requested by f that agreed
// does not grant, for logging the ack diff.
func (f SubscriptionFilter) missingFrom(agreed SubscriptionFilter) []string {
	var missing []string
	add := func(cond, granted bool, name string) {
		if cond && !granted {
			missing = append(missing, name)
		}
	}
	add(f.ToolsListChanged, agreed.ToolsListChanged, "toolsListChanged")
	add(f.PromptsListChanged, agreed.PromptsListChanged, "promptsListChanged")
	add(f.ResourcesListChanged, agreed.ResourcesListChanged, "resourcesListChanged")
	granted := make(map[string]bool, len(agreed.ResourceSubscriptions))
	for _, uri := range agreed.ResourceSubscriptions {
		granted[uri] = true
	}
	for _, uri := range f.ResourceSubscriptions {
		if !granted[uri] {
			missing = append(missing, "resourceSubscriptions:"+uri)
		}
	}
	return missing
}

// Subscription is one open subscriptions/listen stream. The zero value is
// not usable; instances come from Client.ListenSubscriptions.
type Subscription struct {
	client    *Client
	id        *ID
	requested SubscriptionFilter

	cancelCtx  context.Context
	cancelFunc context.CancelFunc

	acked atomic.Bool
	ackCh chan struct{} // closed exactly once on ack

	mu     sync.Mutex // guards agreed + endErr
	agreed SubscriptionFilter
	endErr error

	cancelled atomic.Bool
	endOnce   sync.Once
	done      chan struct{} // closed exactly once on stream end
}

// ID returns the normalized JSON form of the listen request id (the
// subscriptionId servers echo back in _meta).
func (s *Subscription) ID() string { return subscriptionIDKey(s.id) }

// Done is closed when the stream ends, for any reason.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Err returns the terminal error, or nil for a graceful server closure.
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endErr
}

// Agreed returns the filter subset the server acknowledged.
func (s *Subscription) Agreed() SubscriptionFilter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agreed
}

// Requested returns the filter that was originally requested.
func (s *Subscription) Requested() SubscriptionFilter { return s.requested }

// Cancel tears the subscription down: it sends notifications/cancelled on a
// detached goroutine (mirroring the request-path cancellation contract),
// unblocks the transport watch goroutine, and ends the stream. Idempotent.
func (s *Subscription) Cancel(cause error) {
	if !s.cancelled.CompareAndSwap(false, true) {
		return
	}
	id := s.id
	client := s.client
	safego.Go("mcp.subs.cancelled", func() {
		client.notifyCancelled(id, cause, MethodSubscriptionsListen)
	})
	s.end(nil)
}

// end terminates the stream bookkeeping. Called exactly once via endOnce;
// also cancels the subscription-lifetime context so the transport watch
// goroutine (blocked in the HTTP roundtrip or on the waiter channel) exits.
func (s *Subscription) end(err error) {
	s.endOnce.Do(func() {
		if err != nil {
			s.mu.Lock()
			s.endErr = err
			s.mu.Unlock()
		}
		s.cancelFunc()
		close(s.done)
		s.client.removeSubscription(subscriptionIDKey(s.id), s)
	})
}

// markAcked records the acknowledged filter and releases the ack waiter.
// Returns false if the subscription is already acked (duplicate acks are
// idempotent per protocol tolerance of late duplicates).
func (s *Subscription) markAcked(agreed SubscriptionFilter) {
	if !s.acked.CompareAndSwap(false, true) {
		return
	}
	s.mu.Lock()
	s.agreed = agreed
	s.mu.Unlock()
	for _, name := range s.requested.missingFrom(agreed) {
		debug.Log("mcp-subs", "server=%s subscription %s: server did not grant %s",
			s.client.name, s.ID(), name)
	}
	close(s.ackCh)
}

// subscriptionIDKey normalizes a JSON-RPC id to its compact JSON form so a
// numeric id like 7 in a request matches "7" echoed back in _meta
// (json.Number/float ambiguity is removed by Compact).
func subscriptionIDKey(id *ID) string {
	if id == nil {
		return ""
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return ""
	}
	return string(raw)
}

func normalizeIDJSON(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", false
	}
	key := buf.String()
	if key == "" || key == "null" {
		return "", false
	}
	return key, true
}

// subState values for Client.subListenState.
const (
	subStateUnknown = iota
	subStateSupported
	subStateUnsupported
)

// subscriptionsAckTimeout bounds the subscriptions/listen ack wait on the
// synchronous connect path. A compliant server acks immediately; a legacy
// one answers -32601. The bound exists for the third kind - servers that
// ignore unknown methods entirely - which would otherwise stall the connect
// goroutine for the full deadline (10s before this constant existed; live
// freeze capture 2026-09-18). 2s is generous for an ack roundtrip on either
// transport while keeping worst-case connect-path cost at 2s per server.
const SubscriptionsAckTimeout = 2 * time.Second

// EnableModernSubscriptions attempts to open the default list-change
// subscription stream and records whether the server speaks the protocol at
// all. A -32601 (method not found) response downgrades permanently for this
// client instance — legacy servers must never see the call again, matching
// the GET-stream 405 downgrade behavior for HTTP. Safe to call on every
// (re)connect: an existing live subscription short-circuits.
func (c *Client) EnableModernSubscriptions(ctx context.Context) bool {
	if c.subListenState.Load() == subStateUnsupported {
		return false
	}
	c.subMu.Lock()
	existing := c.modernSub
	c.subMu.Unlock()
	if existing != nil {
		select {
		case <-existing.done:
		default:
			return true // already listening
		}
	}

	filter := SubscriptionFilter{
		ToolsListChanged:     true,
		PromptsListChanged:   true,
		ResourcesListChanged: true,
	}
	sub, err := c.ListenSubscriptions(ctx, filter)
	if err != nil {
		var rpcErr *Error
		if errors.As(err, &rpcErr) && rpcErr.Code == errCodeMethodNotFound {
			c.subListenState.Store(subStateUnsupported)
			debug.Log("mcp-subs", "server=%s subscriptions/listen unsupported, using legacy push", c.name)
			return false
		}
		// Deadline-exceeded also downgrades permanently for this client:
		// servers that silently IGNORE unknown methods (Pen.app stdio,
		// 2026-09-18 live capture) never answer -32601, so the deadline is
		// the only signal. Without this the downgrade never sticks and
		// every reconnect re-pays the full ack wait on the synchronous
		// connect path (startup freeze, two Pen servers = 2x10s).
		if errors.Is(err, context.DeadlineExceeded) {
			c.subListenState.Store(subStateUnsupported)
			debug.Log("mcp-subs", "server=%s subscriptions/listen ack deadline exceeded (silent server), downgrading to legacy push", c.name)
			return false
		}
		debug.Log("mcp-subs", "server=%s subscriptions/listen failed: %v", c.name, err)
		return false
	}
	c.subMu.Lock()
	c.modernSub = sub
	c.subMu.Unlock()
	c.subListenState.Store(subStateSupported)
	debug.Log("mcp-subs", "server=%s subscription %s active", c.name, sub.ID())
	return true
}

// ListenSubscriptions opens one correlated notification stream with the
// given filter and blocks until the server acknowledges it (or the wait
// fails). The returned Subscription stays open after this call; its Done
// channel reports the eventual graceful closure or terminal error. The
// request deliberately bypasses mcpRequestTimeout: a compliant stream is
// long-lived by design and its lifetime is governed by the subscription,
// not the caller's ack-wait context.
func (c *Client) ListenSubscriptions(ctx context.Context, filter SubscriptionFilter) (*Subscription, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("mcp[%s]: connection closed", c.name)
	}
	if filter.empty() {
		return nil, fmt.Errorf("mcp[%s]: subscriptions/listen requires a non-empty filter", c.name)
	}
	transport := c.transport
	switch transport {
	case "", "stdio", "http":
	default:
		return nil, fmt.Errorf("mcp[%s]: subscriptions/listen unsupported on transport %q", c.name, transport)
	}

	reqID := c.nextRequestID()
	params, err := json.Marshal(struct {
		Notifications SubscriptionFilter `json:"notifications"`
	}{Notifications: filter})
	if err != nil {
		return nil, fmt.Errorf("mcp[%s]: marshal subscriptions/listen params: %w", c.name, err)
	}
	req := Request{
		JSONRPC: "2.0",
		Method:  MethodSubscriptionsListen,
		Params:  params,
		ID:      reqID,
	}

	sub := &Subscription{
		client:    c,
		id:        reqID,
		requested: filter,
		ackCh:     make(chan struct{}),
		done:      make(chan struct{}),
	}
	sub.cancelCtx, sub.cancelFunc = context.WithCancel(context.Background())

	// stdio: register the waiter BEFORE the write (same ordering guarantee
	// as #994) so the shared read loop can never drop the eventual response
	// as unknown-ID traffic. HTTP: the watch goroutine owns the roundtrip
	// directly — the response arrives as the terminal event of the POST.
	var waiter chan *Response
	if transport == "" || transport == "stdio" {
		waiter = make(chan *Response, 1)
		c.registerWaiter(reqID, waiter)
		c.mu.Lock()
		err = c.writeMessageUnlocked(req)
		c.mu.Unlock()
		if err != nil {
			c.unregisterWaiter(reqID, waiter)
			return nil, fmt.Errorf("mcp[%s]: write subscriptions/listen: %w", c.name, err)
		}
	}

	c.addSubscription(subscriptionIDKey(reqID), sub)

	safego.Go("mcp.subs.watch", func() {
		defer sub.end(nil)
		if waiter != nil {
			defer c.unregisterWaiter(reqID, waiter)
			select {
			case resp, ok := <-waiter:
				if !ok || resp == nil {
					return
				}
				if resp.IsError() {
					sub.end(resp.Error)
				}
				// Graceful closure ("resultType": "complete" or empty result).
			case <-sub.cancelCtx.Done():
			case <-c.notificationDone: // nil-safe: nil chan blocks forever; Close/Abort always cancels cancelCtx via closeAllSubscriptions
			}
			return
		}
		// HTTP: the long-lived POST returns when the server closes the
		// stream gracefully, drops the connection, or the subscription is
		// cancelled (cancelCtx aborts the in-flight roundtrip).
		resp, err := c.sendHTTP(sub.cancelCtx, req)
		select {
		case <-sub.cancelCtx.Done():
			return // cancelled by us; Cancel already handled the protocol side
		default:
		}
		if err != nil {
			sub.end(err)
			return
		}
		if resp.IsError() {
			sub.end(resp.Error)
		}
	})

	select {
	case <-sub.ackCh:
		return sub, nil
	case <-sub.done:
		if err := sub.Err(); err != nil {
			return nil, fmt.Errorf("mcp[%s]: subscriptions/listen: %w", c.name, err)
		}
		return nil, fmt.Errorf("mcp[%s]: subscriptions/listen ended before acknowledgement", c.name)
	case <-ctx.Done():
		sub.Cancel(ctx.Err())
		return nil, fmt.Errorf("mcp[%s]: subscriptions/listen: %w", c.name, ctx.Err())
	case <-time.After(mcpSubscriptionAckTimeout):
		cause := fmt.Errorf("no %s within %s", NotificationSubscriptionsAcknowledged, mcpSubscriptionAckTimeout)
		sub.Cancel(cause)
		return nil, fmt.Errorf("mcp[%s]: subscriptions/listen: %w", c.name, cause)
	}
}

// addSubscription registers sub in the correlation registry.
func (c *Client) addSubscription(key string, sub *Subscription) {
	c.subMu.Lock()
	if c.subs == nil {
		c.subs = make(map[string]*Subscription)
	}
	c.subs[key] = sub
	c.subMu.Unlock()
}

// removeSubscription drops sub from the registry if it is still the entry.
func (c *Client) removeSubscription(key string, sub *Subscription) {
	c.subMu.Lock()
	if c.subs != nil && c.subs[key] == sub {
		delete(c.subs, key)
	}
	c.subMu.Unlock()
}

// closeAllSubscriptions ends every open subscription with cause. Called from
// Abort (once) when the transport is torn down; no notifications/cancelled
// is sent on this path — the connection is gone.
func (c *Client) closeAllSubscriptions(cause error) {
	c.subMu.Lock()
	subs := make([]*Subscription, 0, len(c.subs))
	for _, s := range c.subs {
		subs = append(subs, s)
	}
	c.subs = nil
	c.subMu.Unlock()
	for _, s := range subs {
		s.end(cause)
	}
}

// routeSubscriptionNotification consumes subscription control traffic and
// gates listen-stream notifications. Returns true when the notification has
// been fully handled here and must NOT reach the legacy dispatch
// (cacheInvalidateForNotification / user handler):
//   - notifications/subscriptions/acknowledged: control message, always consumed;
//   - any notification carrying _meta subscriptionId for an unknown or
//     unacknowledged subscription: protocol violation, dropped + logged;
//   - any notification carrying a subscriptionId for a live, acked
//     subscription: correlation complete — but the notification itself is
//     forwarded normally (cache invalidation + user handler must still fire).
//
// Notifications without the _meta key are legacy traffic and fall through.
func (c *Client) routeSubscriptionNotification(notif *Notification) bool {
	if notif.Method == NotificationSubscriptionsAcknowledged {
		key, agreed, ok := parseSubscriptionAckParams(notif.Params)
		if !ok {
			debug.Log("mcp-subs", "server=%s malformed %s params dropped", c.name, notif.Method)
			return true
		}
		c.subMu.Lock()
		sub := c.subs[key]
		c.subMu.Unlock()
		if sub == nil {
			debug.Log("mcp-subs", "server=%s %s for unknown subscription %s dropped",
				c.name, notif.Method, key)
			return true
		}
		sub.markAcked(agreed)
		debug.Log("mcp-subs", "server=%s subscription %s acknowledged", c.name, key)
		return true
	}

	key, ok := extractSubscriptionID(notif.Params)
	if !ok {
		return false
	}
	c.subMu.Lock()
	sub := c.subs[key]
	c.subMu.Unlock()
	if sub == nil || !sub.acked.Load() {
		debug.Log("mcp-subs", "server=%s notification %s for unknown/unacked subscription %s dropped",
			c.name, notif.Method, key)
		return true
	}
	return false
}

// parseSubscriptionAckParams decodes the acknowledged notification envelope:
// {"_meta": {"io.modelcontextprotocol/subscriptionId": <id>},
//
//	"notifications": <filter>}. A missing notifications field yields an empty
//
// agreed filter (be liberal: full agreement).
func parseSubscriptionAckParams(params json.RawMessage) (string, SubscriptionFilter, bool) {
	if len(params) == 0 {
		return "", SubscriptionFilter{}, false
	}
	var envelope struct {
		Meta          map[string]json.RawMessage `json:"_meta"`
		Notifications SubscriptionFilter         `json:"notifications"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return "", SubscriptionFilter{}, false
	}
	raw, ok := envelope.Meta[MetaKeySubscriptionID]
	if !ok {
		return "", SubscriptionFilter{}, false
	}
	key, ok := normalizeIDJSON(raw)
	if !ok {
		return "", SubscriptionFilter{}, false
	}
	return key, envelope.Notifications, true
}

// extractSubscriptionID reports whether params carries the listen-stream
// correlation key, returning its normalized form.
func extractSubscriptionID(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return "", false
	}
	raw, ok := envelope.Meta[MetaKeySubscriptionID]
	if !ok {
		return "", false
	}
	return normalizeIDJSON(raw)
}
