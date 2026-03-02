// Package websocket is a drop-in shim replacing nhooyr.io/websocket with
// gorilla/websocket. Only the subset of the API used by wormhole-william's
// rendezvous client is implemented.
package websocket

import (
	"context"
	"log"
	"net/http"

	gorilla "github.com/gorilla/websocket"
)

// MessageType mirrors the nhooyr.io/websocket constants.
type MessageType int

const (
	MessageText   MessageType = MessageType(gorilla.TextMessage)
	MessageBinary MessageType = MessageType(gorilla.BinaryMessage)
)

// StatusCode mirrors the nhooyr.io/websocket close status codes.
type StatusCode int

const StatusNormalClosure StatusCode = gorilla.CloseNormalClosure

// DialOptions exists for API compatibility; wormhole-william always passes nil.
type DialOptions struct{}

// Conn wraps a gorilla WebSocket connection.
type Conn struct {
	gc *gorilla.Conn
}

// Dial opens a WebSocket connection to urlStr. opts is ignored (always nil from
// wormhole-william).
func Dial(ctx context.Context, urlStr string, opts *DialOptions) (*Conn, *http.Response, error) {
	log.Printf("wshim: dialing %s", urlStr)
	gc, resp, err := gorilla.DefaultDialer.DialContext(ctx, urlStr, nil)
	if err != nil {
		log.Printf("wshim: dial error: %v", err)
		return nil, resp, err
	}
	log.Printf("wshim: connected")
	return &Conn{gc: gc}, resp, nil
}

// Read reads a single message from the connection. It returns when a message
// arrives or ctx is cancelled (closing the underlying connection on cancel).
func (c *Conn) Read(ctx context.Context) (MessageType, []byte, error) {
	type result struct {
		mt  int
		msg []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		mt, msg, err := c.gc.ReadMessage()
		ch <- result{mt, msg, err}
	}()
	select {
	case r := <-ch:
		return MessageType(r.mt), r.msg, r.err
	case <-ctx.Done():
		c.gc.Close()
		return 0, nil, ctx.Err()
	}
}

// WriteJSON sends v as a JSON text message.
func (c *Conn) WriteJSON(v interface{}) error {
	return c.gc.WriteJSON(v)
}

// Close sends a WebSocket close frame and closes the connection.
func (c *Conn) Close(code StatusCode, reason string) error {
	msg := gorilla.FormatCloseMessage(int(code), reason)
	_ = c.gc.WriteMessage(gorilla.CloseMessage, msg)
	return c.gc.Close()
}
