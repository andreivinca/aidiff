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

// Conn is one authenticated WebSocket session with a T3 Code server. Its
// methods are the server operations aidiff uses; a Session owns its lifetime.
//
// Writes: a request is written under the caller's context, so a caller's
// deadline bounds a large upload too. The websocket library closes the
// connection when a context ends mid-frame, since a half-written frame
// corrupts the stream. Control frames (acks, pings, interrupts) never block
// the goroutine that produces them: they queue for a writer of their own,
// which bounds each write by controlWriteTimeout.
type Conn struct {
	ws     *websocket.Conn
	ctx    context.Context // canceled when the connection closes
	cancel context.CancelFunc

	// writeTurn serializes frames. It is a channel rather than a mutex so a
	// writer waits for its turn under its own context, apart from the
	// context bounding the write itself.
	writeTurn chan struct{}
	control   controlQueue
	alive     liveness

	mu      sync.Mutex
	nextID  uint64
	pending map[string]*call
	cause   error // why the client ended the connection, if it did
	err     error // set once the read loop ends
	done    chan struct{}
}

// call routes the frames answering one request.
//
// A stream's chunks are delivered under mu, and finish takes mu too, so once
// finish returns no chunk callback is running or will run again: a stream's
// callback never outlives the subscribe call that registered it.
type call struct {
	mu       sync.Mutex
	finished bool
	chunk    func(values []json.RawMessage) // nil for unary calls
	result   chan outcome                   // receives exactly one outcome
}

// outcome is how a request ended: the server's exit, or a client-side error.
type outcome struct {
	exit rpcExit
	err  error
}

// deliver hands a chunk to the stream's callback unless the stream is
// finished, reporting whether it did.
func (cl *call) deliver(values []json.RawMessage) bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.finished || cl.chunk == nil {
		return false
	}
	cl.chunk(values)
	return true
}

// finish marks the call over, waiting out a chunk callback in progress.
func (cl *call) finish() {
	cl.mu.Lock()
	cl.finished = true
	cl.mu.Unlock()
}

// frameHeader is the part of every server frame that routes it. It is
// decoded on its own so a frame whose body does not decode still fails the
// request it answers.
type frameHeader struct {
	Tag       string `json:"_tag"`
	RequestID string `json:"requestId"`
}

// dial opens an authenticated session with the server at ep.
func dial(ctx context.Context, ep endpoint, token string) (*Conn, error) {
	wsURL, err := socketURL(ep.base)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	ws, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: ep.client, HTTPHeader: hdr})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // the upgrade response carries nothing further
	}
	if err != nil {
		return nil, dialError(ep.base, resp, err)
	}
	ws.SetReadLimit(maxFrameBytes)

	cctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		ws:        ws,
		ctx:       cctx,
		cancel:    cancel,
		writeTurn: make(chan struct{}, 1),
		control:   newControlQueue(),
		pending:   map[string]*call{},
		done:      make(chan struct{}),
	}
	go c.readLoop()
	go c.controlLoop()
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
		return fmt.Errorf("T3 at %s: HTTP %d: %w", baseURL, resp.StatusCode, errUnauthorized)
	case http.StatusUpgradeRequired:
		return fmt.Errorf("T3 at %s speaks a different orchestration protocol than this aidiff (%s); update one of them", baseURL, protocolVersion)
	}
	return fmt.Errorf("connect to T3 at %s: HTTP %d: %w", baseURL, resp.StatusCode, err)
}

// closeHandshakeTimeout bounds the close handshake: a peer gone without a
// word (a sleeping laptop, a dropped VPN) never answers it, and the
// websocket library would wait 5s for that.
const closeHandshakeTimeout = time.Second

// close ends the connection. Session.Close is the public way to do it, since
// a session may own an SSH tunnel as well.
func (c *Conn) close() error {
	// canceling the read loop's context closes the socket, which ends a
	// handshake still waiting for the peer
	stop := time.AfterFunc(closeHandshakeTimeout, c.cancel)
	err := c.ws.Close(websocket.StatusNormalClosure, "")
	stop.Stop()
	c.cancel()
	if err != nil {
		return fmt.Errorf("close T3 connection: %w", err)
	}
	return nil
}

// abort ends the connection because of cause, which Err then reports.
func (c *Conn) abort(cause error) {
	c.mu.Lock()
	if c.cause == nil {
		c.cause = cause
	}
	c.mu.Unlock()
	_ = c.ws.CloseNow()
	c.cancel()
}

// Done is closed when the connection ends; Err then reports why.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Err reports why the connection ended, or nil while it is open.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// call sends a unary request and decodes its success value into out (may be nil).
func (c *Conn) call(ctx context.Context, method string, payload, out any) error {
	cl := &call{result: make(chan outcome, 1)}
	id := c.newID()
	if err := c.send(ctx, id, method, payload, cl); err != nil {
		return err
	}
	return c.await(ctx, method, id, cl, out)
}

// subscribe opens a server stream. onValue receives each streamed value in
// order, on the connection's read goroutine, so it must not block. subscribe
// returns when the stream ends, the connection drops, or ctx is canceled;
// onValue is never called after it returns.
func (c *Conn) subscribe(ctx context.Context, method string, payload any, onValue func(json.RawMessage)) error {
	id := c.newID()
	cl := &call{result: make(chan outcome, 1)}
	cl.chunk = func(values []json.RawMessage) {
		for _, v := range values {
			onValue(v)
		}
	}
	if err := c.send(ctx, id, method, payload, cl); err != nil {
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

func (c *Conn) send(ctx context.Context, id, method string, payload any, cl *call) error {
	if payload == nil {
		payload = struct{}{}
	}
	frame, err := json.Marshal(map[string]any{
		"_tag": "Request", "id": id, "tag": method, "payload": payload, "headers": [][2]string{},
	})
	if err != nil {
		return fmt.Errorf("t3 %s: encode request: %w", method, err)
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("t3 %s: %w", method, err)
	}
	c.pending[id] = cl
	c.mu.Unlock()
	if err := c.write(ctx, ctx, frame); err != nil {
		c.forget(id)
		return fmt.Errorf("t3 %s: %w", method, err)
	}
	return nil
}

// await waits for the request's outcome. Whichever way it ends, the call is
// finished before await returns, so a stream's callback cannot run after.
func (c *Conn) await(ctx context.Context, method, id string, cl *call, out any) error {
	defer cl.finish()
	select {
	case res := <-cl.result:
		if res.err != nil {
			return fmt.Errorf("t3 %s: %w", method, res.err)
		}
		return decodeExit(method, res.exit, out)
	case <-c.done:
		c.forget(id)
		return fmt.Errorf("t3 %s: %w", method, c.Err())
	case <-ctx.Done():
		c.forget(id)
		c.control.push(controlFrame{Tag: "Interrupt", RequestID: id})
		return fmt.Errorf("t3 %s: %w", method, ctx.Err())
	}
}

func (c *Conn) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// take removes and returns the pending call with id, nil when there is none.
func (c *Conn) take(id string) *call {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl := c.pending[id]
	delete(c.pending, id)
	return cl
}

// write sends one frame: it waits for its turn under turnCtx, then writes
// under writeCtx. An error while waiting leaves the connection intact; one
// during the write closes it (see Conn).
func (c *Conn) write(turnCtx, writeCtx context.Context, frame []byte) error {
	select {
	case c.writeTurn <- struct{}{}:
	case <-turnCtx.Done():
		return fmt.Errorf("write frame: %w", turnCtx.Err())
	case <-c.ctx.Done():
		return errors.New("write frame: connection closed")
	}
	defer func() {
		<-c.writeTurn
	}()
	if err := c.ws.Write(writeCtx, websocket.MessageText, frame); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

func (c *Conn) readLoop() {
	var err error
	defer func() {
		c.mu.Lock()
		switch {
		case c.cause != nil:
			err = c.cause
		case err == nil || errors.Is(err, context.Canceled):
			err = errors.New("connection closed")
		}
		c.err = err
		c.mu.Unlock()
		c.cancel() // release writers waiting for their turn
		close(c.done)
	}()
	for {
		var data []byte
		_, data, err = c.ws.Read(c.ctx)
		if err != nil {
			return
		}
		c.alive.heard()
		c.dispatch(data)
	}
}

// dispatch routes one server frame. Effect RPC servers send Chunk and Exit
// for a request, Pong for a ping, and Defect when the server itself failed;
// a Defect ends every request, so it ends the connection. A frame naming a
// pending request that cannot be read, or whose tag is unknown, fails that
// request rather than leaving it waiting.
func (c *Conn) dispatch(data []byte) {
	var head frameHeader
	if json.Unmarshal(data, &head) != nil {
		return // not a frame at all: it answers nothing we could identify
	}
	switch head.Tag {
	case "Chunk":
		c.dispatchChunk(head.RequestID, data)
	case "Exit":
		c.dispatchExit(head.RequestID, data)
	case "Defect":
		c.abort(defectError(data))
	case "ClientProtocolError":
		c.abort(fmt.Errorf("T3 protocol error: %s", compactJSON(data)))
	case "Pong":
	default:
		c.failStream(head.RequestID, fmt.Errorf("unexpected %q frame from the server", head.Tag))
	}
}

func (c *Conn) dispatchChunk(id string, data []byte) {
	var msg struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		c.failStream(id, fmt.Errorf("unreadable stream chunk: %w", err))
		return
	}
	c.mu.Lock()
	cl := c.pending[id]
	c.mu.Unlock()
	if cl == nil || !cl.deliver(msg.Values) {
		return
	}
	// the server holds a stream back after a few unacknowledged chunks
	c.control.push(controlFrame{Tag: "Ack", RequestID: id})
}

func (c *Conn) dispatchExit(id string, data []byte) {
	cl := c.take(id)
	if cl == nil {
		return
	}
	var msg struct {
		Exit rpcExit `json:"exit"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		cl.result <- outcome{err: fmt.Errorf("unreadable result: %w", err)}
		return
	}
	cl.result <- outcome{exit: msg.Exit}
}

// failStream fails the pending request id, if any, and interrupts it on the
// server, whose side of it may still be running.
func (c *Conn) failStream(id string, err error) {
	cl := c.take(id)
	if cl == nil {
		return
	}
	cl.result <- outcome{err: err}
	c.control.push(controlFrame{Tag: "Interrupt", RequestID: id})
}
