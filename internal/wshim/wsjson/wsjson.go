// Package wsjson is a drop-in shim for nhooyr.io/websocket/wsjson.
// Only wsjson.Write is implemented, which is all wormhole-william uses.
package wsjson

import (
	"context"

	"nhooyr.io/websocket"
)

// Write marshals v as JSON and sends it as a WebSocket text message.
func Write(ctx context.Context, c *websocket.Conn, v interface{}) error {
	return c.WriteJSON(v)
}
