package t3client

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// holdWriteTurn takes c's write turn, as a large upload in progress would,
// until the returned release is called.
func holdWriteTurn(t *testing.T, c *Conn) (release func()) {
	t.Helper()
	c.writeTurn <- struct{}{}
	return func() {
		<-c.writeTurn
	}
}

func TestConn_CallWaitingForTheWriterHonorsItsContext(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		send(t, ws, success(req["id"], nil))
	})
	c := dialFake(t, url)
	release := holdWriteTurn(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, c.call(ctx, "x", nil, nil), context.DeadlineExceeded)
	release()
	require.NoError(t, c.call(context.Background(), "x", nil, nil), "a request that never got its turn leaves the connection intact")
}

func TestConn_AcksNeverStallTheReadLoop(t *testing.T) {
	stream := make(chan string, 1)
	f, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] == "stream" {
			stream <- req["id"].(string)
			return
		}
		send(t, ws, success(req["id"], nil))
	})
	c := dialFake(t, url)
	var vals []string
	done := make(chan error, 1)
	go func() {
		done <- c.subscribe(context.Background(), "stream", nil, func(v json.RawMessage) {
			vals = append(vals, string(v))
		})
	}()
	id := <-stream
	release := holdWriteTurn(t, c) // no frame can be written now, an ack included
	ws := f.socket()
	for i := range 3 {
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": id, "values": []any{i}})
	}
	send(t, ws, success(id, nil))
	require.NoError(t, <-done, "the stream ends while its acks wait")
	assert.Equal(t, []string{"0", "1", "2"}, vals)
	assert.Equal(t, 0, f.ackCount())
	release()
	require.Eventually(t, func() bool {
		return f.ackCount() == 3
	}, 2*time.Second, 10*time.Millisecond, "the queued acks go out once the writer is free")
}

func TestConn_UnreadableExitFailsItsCall(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		switch req["tag"] {
		case "bad-exit":
			send(t, ws, map[string]any{"_tag": "Exit", "requestId": req["id"], "exit": "not an exit"})
		case "mystery":
			send(t, ws, map[string]any{"_tag": "FromTheFuture", "requestId": req["id"]})
		default:
			send(t, ws, success(req["id"], nil))
		}
	})
	c := dialFake(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorContains(t, c.call(ctx, "bad-exit", nil, nil), "unreadable result")
	require.ErrorContains(t, c.call(ctx, "mystery", nil, nil), `unexpected "FromTheFuture" frame`)
	require.NoError(t, c.call(ctx, "fine", nil, nil), "the connection survives both")
}

func TestConn_DefectEndsTheConnection(t *testing.T) {
	_, url := startFake(t, func(ws *websocket.Conn, _ map[string]any) {
		send(t, ws, map[string]any{"_tag": "Defect", "defect": "server exploded"})
	})
	c := dialFake(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorContains(t, c.call(ctx, "x", nil, nil), "server exploded")
	<-c.Done()
	require.ErrorContains(t, c.Err(), "T3 server failed: server exploded")
}

func TestConn_SilentServerEndsTheConnection(t *testing.T) {
	_, url := startFake(t, nil)
	c := dialFake(t, url)
	start := time.Now()
	c.alive.pinged(start)
	c.ping(start.Add(silenceLimit / 2))
	select {
	case <-c.Done():
		t.Fatal("a ping that is not yet overdue ends nothing")
	default:
	}
	c.ping(start.Add(silenceLimit))
	<-c.Done()
	require.ErrorContains(t, c.Err(), "stopped answering")
}

func TestLiveness(t *testing.T) {
	var l liveness
	t0 := time.Now()
	assert.Zero(t, l.silence(t0), "no ping, no silence")
	l.pinged(t0)
	l.pinged(t0.Add(time.Second)) // the oldest unanswered ping counts
	assert.Equal(t, 5*time.Second, l.silence(t0.Add(5*time.Second)))
	l.heard()
	assert.Zero(t, l.silence(t0.Add(time.Minute)), "any frame answers")
}

func TestCall_NoChunkAfterFinish(t *testing.T) {
	var got int
	cl := &call{chunk: func([]json.RawMessage) {
		got++
	}}
	assert.True(t, cl.deliver(nil))
	cl.finish()
	assert.False(t, cl.deliver(nil), "a finished stream takes no more chunks")
	assert.Equal(t, 1, got)
}

func TestConn_SubscribeCallbackStopsWithIt(t *testing.T) {
	more, sent := make(chan struct{}), make(chan struct{})
	_, url := startFake(t, func(ws *websocket.Conn, req map[string]any) {
		if req["tag"] != "stream" {
			<-sent
			send(t, ws, success(req["id"], nil))
			return
		}
		id := req["id"]
		send(t, ws, map[string]any{"_tag": "Chunk", "requestId": id, "values": []any{1}})
		go func() {
			<-more
			send(t, ws, map[string]any{"_tag": "Chunk", "requestId": id, "values": []any{2}})
			close(sent)
		}()
	})
	c := dialFake(t, url)
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	err := c.subscribe(ctx, "stream", nil, func(json.RawMessage) {
		calls++
		cancel()
	})
	require.ErrorIs(t, err, context.Canceled)
	close(more)
	// the late chunk is sent before this reply, and frames arrive in order
	require.NoError(t, c.call(context.Background(), "after", nil, nil))
	assert.Equal(t, 1, calls)
}
