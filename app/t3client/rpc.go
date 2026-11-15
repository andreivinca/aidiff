// Package t3client connects aidiff to a T3 Code server (https://github.com/pingdotgg/t3code)
// so a review can run against a T3 thread's worktree, local or remote.
//
// T3 Code serves its API as Effect RPC over a WebSocket at /ws, one JSON
// message per text frame. This package speaks the subset of that protocol
// aidiff needs: unary requests, server streams with flow-control acks, and
// keep-alive pings. The protocol is internal to T3 Code, pinned by an
// orchestration protocol version the server checks at upgrade time.
package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// protocolVersion is the orchestration protocol this client speaks. The server
// rejects the upgrade with HTTP 426 when it does not match.
const protocolVersion = "2"

// maxFrameBytes caps a single inbound frame. Diff previews arrive as one frame,
// so the websocket library's 32 KiB default is far too small.
const maxFrameBytes = 256 << 20

// pingInterval keeps idle connections alive through proxies and tunnels.
const pingInterval = 25 * time.Second

// Conn is one authenticated WebSocket session with a T3 Code server.
type Conn struct {
	ws     *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  uint64
	pending map[string]*call
	err     error // set once the read loop ends
	done    chan struct{}
}

// call routes the frames answering one request.
type call struct {
	chunk func(values []json.RawMessage) // nil for unary calls
	exit  chan rpcExit
}

// inbound is the union of server frames this client reads.
type inbound struct {
	Tag       string            `json:"_tag"`
	RequestID string            `json:"requestId"`
	Values    []json.RawMessage `json:"values"`
	Exit      rpcExit           `json:"exit"`
}

type rpcExit struct {
	Tag   string          `json:"_tag"` // Success | Failure
	Value json.RawMessage `json:"value"`
	Cause json.RawMessage `json:"cause"`
}

// ErrUnauthorized means the server refused the access token.
var ErrUnauthorized = errors.New("access token rejected")

// RPCError is a failed request: a typed server error or a defect.
type RPCError struct {
	Method string
	Tag    string // error tag, e.g. VcsRepositoryDetectionError; empty for defects
	Detail string
}

func (e *RPCError) Error() string {
	if e.Tag == "" {
		return fmt.Sprintf("t3 %s: %s", e.Method, e.Detail)
	}
	return fmt.Sprintf("t3 %s: %s: %s", e.Method, e.Tag, e.Detail)
}

// Dial opens an authenticated session. baseURL is the server's HTTP origin
// (http://host:port or https://host); token is a bearer access token.
func Dial(ctx context.Context, baseURL, token string) (*Conn, error) {
	wsURL, err := socketURL(baseURL)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	ws, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // the upgrade response carries nothing further
	}
	if err != nil {
		return nil, dialError(baseURL, resp, err)
	}
	ws.SetReadLimit(maxFrameBytes)

	cctx, cancel := context.WithCancel(context.Background())
	c := &Conn{ws: ws, ctx: cctx, cancel: cancel, pending: map[string]*call{}, done: make(chan struct{})}
	go c.readLoop()
	go c.pingLoop()
	return c, nil
}

// socketURL turns an HTTP origin into the /ws endpoint with the protocol pin.
func socketURL(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid T3 server URL %q", baseURL)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("invalid T3 server URL %q: scheme must be http or https", baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	u.RawQuery = url.Values{"orchestrationProtocol": {protocolVersion}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}

// dialError explains the upgrade failures a user can act on.
func dialError(baseURL string, resp *http.Response, err error) error {
	if resp == nil {
		return fmt.Errorf("connect to T3 at %s: %w", baseURL, err)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("T3 at %s: HTTP %d: %w", baseURL, resp.StatusCode, ErrUnauthorized)
	case http.StatusUpgradeRequired:
		return fmt.Errorf("T3 at %s speaks a different orchestration protocol than this aidiff (%s); update one of them", baseURL, protocolVersion)
	}
	return fmt.Errorf("connect to T3 at %s: HTTP %d: %w", baseURL, resp.StatusCode, err)
}

// Close ends the session.
func (c *Conn) Close() error {
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	c.cancel()
	if err != nil {
		return fmt.Errorf("close T3 connection: %w", err)
	}
	return nil
}

// Done is closed when the connection ends; Err then reports why.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err reports why the connection ended, or nil while it is open.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Call sends a unary request and decodes its success value into out (may be nil).
func (c *Conn) Call(ctx context.Context, method string, payload, out any) error {
	cl := &call{exit: make(chan rpcExit, 1)}
	id := c.newID()
	if err := c.send(id, method, payload, cl); err != nil {
		return err
	}
	return c.await(ctx, method, id, cl, out)
}

// Subscribe opens a server stream. onValue receives each streamed value in
// order, on the connection's read goroutine, so it must not block. Subscribe
// returns when the stream ends, the connection drops, or ctx is canceled.
func (c *Conn) Subscribe(ctx context.Context, method string, payload any, onValue func(json.RawMessage)) error {
	id := c.newID()
	cl := &call{exit: make(chan rpcExit, 1)}
	cl.chunk = func(values []json.RawMessage) {
		for _, v := range values {
			onValue(v)
		}
		// the server holds a stream back after a few unacknowledged chunks
		_ = c.write(map[string]any{"_tag": "Ack", "requestId": id})
	}
	if err := c.send(id, method, payload, cl); err != nil {
		return err
	}
	return c.await(ctx, method, id, cl, nil)
}

func (c *Conn) newID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return strconv.FormatUint(c.nextID, 10)
}

func (c *Conn) send(id, method string, payload any, cl *call) error {
	if payload == nil {
		payload = struct{}{}
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("t3 %s: %w", method, err)
	}
	c.pending[id] = cl
	c.mu.Unlock()
	err := c.write(map[string]any{
		"_tag": "Request", "id": id, "tag": method, "payload": payload, "headers": [][2]string{},
	})
	if err != nil {
		c.forget(id)
		return fmt.Errorf("t3 %s: %w", method, err)
	}
	return nil
}

func (c *Conn) await(ctx context.Context, method, id string, cl *call, out any) error {
	select {
	case ex := <-cl.exit:
		return decodeExit(method, ex, out)
	case <-c.done:
		c.forget(id)
		return fmt.Errorf("t3 %s: %w", method, c.Err())
	case <-ctx.Done():
		c.forget(id)
		_ = c.write(map[string]any{"_tag": "Interrupt", "requestId": id})
		return fmt.Errorf("t3 %s: %w", method, ctx.Err())
	}
}

func (c *Conn) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) write(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.ws.Write(c.ctx, websocket.MessageText, b); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

func (c *Conn) readLoop() {
	var err error
	defer func() {
		c.mu.Lock()
		if err == nil || errors.Is(err, context.Canceled) {
			err = errors.New("connection closed")
		}
		c.err = err
		c.mu.Unlock()
		close(c.done)
	}()
	for {
		var data []byte
		_, data, err = c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		var msg inbound
		if json.Unmarshal(data, &msg) != nil {
			continue // a frame we cannot read answers nothing we wait on
		}
		c.dispatch(msg)
	}
}

func (c *Conn) dispatch(msg inbound) {
	switch msg.Tag {
	case "Chunk":
		c.mu.Lock()
		cl := c.pending[msg.RequestID]
		c.mu.Unlock()
		if cl != nil && cl.chunk != nil {
			cl.chunk(msg.Values)
		}
	case "Exit":
		c.mu.Lock()
		cl := c.pending[msg.RequestID]
		delete(c.pending, msg.RequestID)
		c.mu.Unlock()
		if cl != nil {
			cl.exit <- msg.Exit
		}
	}
}

func (c *Conn) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			_ = c.write(map[string]string{"_tag": "Ping"})
		}
	}
}

// decodeExit turns an Exit frame into the call's result.
func decodeExit(method string, ex rpcExit, out any) error {
	if ex.Tag == "Success" {
		if out == nil || len(ex.Value) == 0 || string(ex.Value) == "null" {
			return nil
		}
		if err := json.Unmarshal(ex.Value, out); err != nil {
			return fmt.Errorf("t3 %s: decode result: %w", method, err)
		}
		return nil
	}
	return failureError(method, ex.Cause)
}

// failureError reads the first entry of an Effect failure cause.
func failureError(method string, raw json.RawMessage) error {
	var causes []struct {
		Tag    string          `json:"_tag"` // Fail | Die | Interrupt
		Error  json.RawMessage `json:"error"`
		Defect json.RawMessage `json:"defect"`
	}
	if err := json.Unmarshal(raw, &causes); err != nil || len(causes) == 0 {
		return &RPCError{Method: method, Detail: "request failed"}
	}
	c := causes[0]
	switch c.Tag {
	case "Fail":
		var e struct {
			Tag     string `json:"_tag"`
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(c.Error, &e)
		detail := e.Detail
		if detail == "" {
			detail = e.Message
		}
		if detail == "" {
			detail = string(c.Error)
		}
		return &RPCError{Method: method, Tag: e.Tag, Detail: detail}
	case "Die":
		var s string
		if json.Unmarshal(c.Defect, &s) != nil {
			s = string(c.Defect)
		}
		return &RPCError{Method: method, Detail: s}
	}
	return &RPCError{Method: method, Detail: strings.ToLower(c.Tag)}
}
