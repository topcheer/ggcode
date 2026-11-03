package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type Transport struct {
	Scanner *bufio.Scanner
	Writer  io.Writer
	mu      sync.Mutex

	outbox     chan outboundMsg
	writerOnce sync.Once
	dropped    atomic.Int64
	stopped    chan struct{}
	sendMu     sync.Mutex
	stopOnce   sync.Once
	writerDone chan struct{}

	nextID    atomic.Int64
	pending   map[int64]chan *JSONRPCResponse
	pendingMu sync.Mutex
}

type outboundMsg struct {
	data []byte
	ack  chan error
}

const outboundDeadline = 10 * time.Second

const outboundQueueCap = 256

func NewTransport(r io.Reader, w io.Writer) *Transport {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	t := &Transport{
		Scanner: s,
		Writer:  w,
		pending: make(map[int64]chan *JSONRPCResponse),
	}
	t.startWriter()
	return t
}

func (t *Transport) WriteRaw(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.Writer.Write(data); err != nil {
		return err
	}
	_, err := t.Writer.Write([]byte("\n"))
	return err
}

func (t *Transport) ReadRaw() ([]byte, error) {
	if !t.Scanner.Scan() {
		if err := t.Scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading raw: %w", err)
		}
		return nil, io.EOF
	}
	return t.Scanner.Bytes(), nil
}

func (t *Transport) CloseWriter() error {
	t.stopWriter()
	select {
	case <-t.writerDone:
	case <-time.After(outboundDeadline):
		debug.Log("acp", "CloseWriter: outbound drain timed out, closing anyway")
	}
	if c, ok := t.Writer.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (t *Transport) ReadMessage() (*JSONRPCRequest, error) {
	if !t.Scanner.Scan() {
		if err := t.Scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading message: %w", err)
		}
		return nil, io.EOF
	}
	line := t.Scanner.Bytes()
	if len(line) == 0 {
		return nil, nil
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return nil, fmt.Errorf("parsing JSON-RPC message: %w", err)
	}
	return &req, nil
}

func (t *Transport) ReadAnyMessage() (*JSONRPCRequest, *JSONRPCResponse, error) {
	if !t.Scanner.Scan() {
		if err := t.Scanner.Err(); err != nil {
			return nil, nil, fmt.Errorf("reading message: %w", err)
		}
		return nil, nil, io.EOF
	}
	line := t.Scanner.Bytes()
	if len(line) == 0 {
		return nil, nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing JSON-RPC message: %w", err)
	}

	if _, hasMethod := raw["method"]; hasMethod {
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return nil, nil, fmt.Errorf("parsing JSON-RPC request: %w", err)
		}
		return &req, nil, nil
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, nil, fmt.Errorf("parsing JSON-RPC response: %w", err)
	}
	if rawResult, ok := raw["result"]; ok {
		resp.RawResult = rawResult
	}
	return nil, &resp, nil
}

func (t *Transport) SendRequest(ctx context.Context, method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	id := t.nextID.Add(1)

	ch := make(chan *JSONRPCResponse, 1)
	t.pendingMu.Lock()
	t.pending[id] = ch
	t.pendingMu.Unlock()
	defer func() {
		t.pendingMu.Lock()
		delete(t.pending, id)
		t.pendingMu.Unlock()
	}()

	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshaling request params: %w", err)
		}
		req.Params = raw
	}

	if err := t.writeJSON(req); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("client error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.RawResult, nil
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for client response to %s", method)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *Transport) FailAllPending(err error) {
	if err == nil {
		err = errors.New("client connection closed")
	}
	failure := &JSONRPCResponse{
		Error: &JSONRPCError{
			Code:    ErrCodeInternalError,
			Message: err.Error(),
		},
	}

	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	for id, ch := range t.pending {
		select {
		case ch <- failure:
		default:
		}
		delete(t.pending, id)
	}
}

func (t *Transport) DeliverResponse(resp *JSONRPCResponse) {
	if resp.ID == nil {
		return
	}

	var id int64
	switch v := resp.ID.(type) {
	case float64:
		id = int64(v)
	case int:
		id = int64(v)
	case int64:
		id = v
	default:
		debug.Log("acp", "DeliverResponse: dropping response with non-numeric id %v (type %T); JSON-RPC 2.0 requires echoing the request id", resp.ID, resp.ID)
		return
	}

	t.pendingMu.Lock()
	ch, ok := t.pending[id]
	t.pendingMu.Unlock()

	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

func (t *Transport) WriteResponse(id RequestID, result interface{}) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return t.writeJSON(resp)
}

func (t *Transport) WriteError(id RequestID, code int, msg string) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
	}
	return t.writeJSON(resp)
}

func (t *Transport) WriteErrorNilID(code int, msg string) error {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
	}
	return t.writeJSON(resp)
}

func (t *Transport) WriteNotification(method string, params interface{}) error {
	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshaling notification params: %w", err)
		}
		notif.Params = raw
	}
	data, err := marshalOutbound(notif)
	if err != nil {
		return err
	}
	t.writeDroppable(data)
	return nil
}

func (t *Transport) startWriter() {
	t.writerOnce.Do(func() {
		t.outbox = make(chan outboundMsg, outboundQueueCap)
		t.stopped = make(chan struct{})
		t.writerDone = make(chan struct{})
		safego.Go("acp.transportWriter", func() {
			defer close(t.writerDone)
			for msg := range t.outbox {
				t.mu.Lock()
				_, werr := t.Writer.Write(msg.data)
				if werr == nil {
					_, werr = t.Writer.Write([]byte("\n"))
				}
				t.mu.Unlock()
				if werr != nil {
					debug.Log("acp", "outbound write failed: %v", werr)
				}
				if msg.ack != nil {
					msg.ack <- werr
				}
			}
		})
	})
}

func (t *Transport) stopWriter() {
	t.startWriter()
	t.stopOnce.Do(func() {
		t.sendMu.Lock()
		close(t.outbox)
		close(t.stopped)
		t.sendMu.Unlock()
	})
}

func marshalOutbound(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling JSON-RPC message: %w", err)
	}
	return data, nil
}

func (t *Transport) writeBlocking(data []byte) error {
	t.startWriter()
	ack := make(chan error, 1)
	deadline := time.NewTimer(outboundDeadline)
	defer deadline.Stop()
	for {
		t.sendMu.Lock()
		select {
		case <-t.stopped:
			t.sendMu.Unlock()
			return fmt.Errorf("transport writer stopped: peer connection closed")
		default:
		}
		select {
		case t.outbox <- outboundMsg{data: data, ack: ack}:
			t.sendMu.Unlock()
			goto queued
		default:
		}
		t.sendMu.Unlock()
		select {
		case <-deadline.C:
			return fmt.Errorf("outbound queue full: peer is not reading (dropped %d notifications so far)", t.dropped.Load())
		case <-time.After(2 * time.Millisecond):
		}
	}
queued:
	select {
	case werr := <-ack:
		if werr != nil {
			return fmt.Errorf("writing JSON-RPC message: %w", werr)
		}
		return nil
	case <-deadline.C:
		return fmt.Errorf("outbound write stalled: peer is not reading")
	}
}

func (t *Transport) writeDroppable(data []byte) {
	t.startWriter()
	t.sendMu.Lock()
	select {
	case <-t.stopped:
		t.sendMu.Unlock()
		n := t.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			debug.Log("acp", "peer is not reading: dropped %d outbound notifications", n)
		}
		return
	default:
	}
	select {
	case t.outbox <- outboundMsg{data: data}:
		t.sendMu.Unlock()
	default:
		t.sendMu.Unlock()
		n := t.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			debug.Log("acp", "peer is not reading: dropped %d outbound notifications", n)
		}
	}
}

func (t *Transport) writeJSON(v interface{}) error {
	data, err := marshalOutbound(v)
	if err != nil {
		return err
	}
	return t.writeBlocking(data)
}
