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

const (
	MethodSubscriptionsListen = "subscriptions/listen"

	NotificationSubscriptionsAcknowledged = "notifications/subscriptions/acknowledged"

	MetaKeySubscriptionID = "io.modelcontextprotocol/subscriptionId"

	mcpSubscriptionAckTimeout = 15 * time.Second
)

const errCodeMethodNotFound = -32601

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

type Subscription struct {
	client    *Client
	id        *ID
	requested SubscriptionFilter

	cancelCtx  context.Context
	cancelFunc context.CancelFunc

	acked atomic.Bool
	ackCh chan struct{}

	mu     sync.Mutex
	agreed SubscriptionFilter
	endErr error

	cancelled atomic.Bool
	endOnce   sync.Once
	done      chan struct{}
}

func (s *Subscription) ID() string { return subscriptionIDKey(s.id) }

func (s *Subscription) Done() <-chan struct{} { return s.done }

func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endErr
}

func (s *Subscription) Agreed() SubscriptionFilter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agreed
}

func (s *Subscription) Requested() SubscriptionFilter { return s.requested }

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

const (
	subStateUnknown = iota
	subStateSupported
	subStateUnsupported
)

const SubscriptionsAckTimeout = 2 * time.Second

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
			return true
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

		if errors.Is(err, context.DeadlineExceeded) {
			c.subListenState.Store(subStateUnsupported)
			debug.Log("mcp-subs", "server=%s subscriptions/listen ack deadline exceeded (silent server), downgrading to legacy push", c.name)
			return false
		}
		debug.Log("mcp-subs", "server=%s subscriptions/listen failed: %v", c.name, err)
		return false
	}
	c.subMu.Lock()

	if cur := c.modernSub; cur != nil {
		select {
		case <-cur.done:

		default:
			c.subMu.Unlock()
			sub.Cancel(fmt.Errorf("subscriptions/listen lost the concurrent-enable race (#3869)"))
			debug.Log("mcp-subs", "server=%s concurrent subscriptions/listen lost the race; cancelling the new one", c.name)
			return true
		}
	}
	c.modernSub = sub
	c.subMu.Unlock()
	c.subListenState.Store(subStateSupported)
	debug.Log("mcp-subs", "server=%s subscription %s active", c.name, sub.ID())
	return true
}

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

	// Register the subscription BEFORE the write (#3817): the stdio
	// server is an independent process and can flush its
	// `notifications/subscriptions/acknowledged` ack the instant the write
	// returns; the read loop correlates acks via c.subs[key], so with
	// addSubscription AFTER the write the ack hit "unknown subscription
	// dropped" (unreplayable) and ackCh hung until the 15s timeout, or the
	// caller's own deadline (2s in mcp_loader) fired first. Same
	// register-before-write ordering guarantee as the waiter above (#994);
	// a failed write rolls the registration back.

	c.addSubscription(subscriptionIDKey(reqID), sub)

	var waiter chan *Response
	if transport == "" || transport == "stdio" {
		waiter = make(chan *Response, 1)
		c.registerWaiter(reqID, waiter)
		c.mu.Lock()
		err = c.writeMessageUnlocked(req)
		c.mu.Unlock()
		if err != nil {
			c.unregisterWaiter(reqID, waiter)
			c.removeSubscription(subscriptionIDKey(reqID), sub)
			return nil, fmt.Errorf("mcp[%s]: write subscriptions/listen: %w", c.name, err)
		}
	}

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

			case <-sub.cancelCtx.Done():
			case <-c.notificationDone:
			}
			return
		}

		resp, err := c.sendHTTP(sub.cancelCtx, req)
		select {
		case <-sub.cancelCtx.Done():
			return
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

func (c *Client) addSubscription(key string, sub *Subscription) {
	c.subMu.Lock()
	if c.subs == nil {
		c.subs = make(map[string]*Subscription)
	}
	c.subs[key] = sub
	c.subMu.Unlock()
}

func (c *Client) removeSubscription(key string, sub *Subscription) {
	c.subMu.Lock()
	if c.subs != nil && c.subs[key] == sub {
		delete(c.subs, key)
	}
	c.subMu.Unlock()
}

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
