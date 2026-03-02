package wormhole_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btraven00/obadm/internal/cache"
	obwormhole "github.com/btraven00/obadm/internal/wormhole"
	"github.com/psanford/wormhole-william/rendezvous/rendezvousservertest"
)

// newTestConfig returns a Config pointing at a local rendezvous server and
// a local transit relay, so tests never touch the public wormhole servers.
func newTestConfig(t *testing.T) obwormhole.Config {
	t.Helper()
	rs := rendezvousservertest.NewServer()
	t.Cleanup(rs.Close)

	return obwormhole.Config{
		RendezvousURL:       rs.WebSocketURL(),
		TransitRelayAddress: startTestRelay(t),
	}
}

// setCacheDir redirects the OS cache directory for the duration of the test
// and returns the temp path so callers can inspect it.
func setCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

// makeSrcRun creates a cache run with the given JSONL content in the current
// XDG_CACHE_HOME. Call this while the sender's cache dir is active.
func makeSrcRun(t *testing.T, content string) *cache.Run {
	t.Helper()
	run, err := cache.New("testhost", "test/telemetry.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	w, err := run.Writer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return run
}

func TestShareReceive(t *testing.T) {
	cfg := newTestConfig(t)

	// Build the source run in a separate cache dir so the receiver doesn't
	// mistake it for a duplicate (in production, sender and receiver are on
	// different machines with different caches).
	senderCache := t.TempDir()
	receiverCache := t.TempDir()

	os.Setenv("XDG_CACHE_HOME", senderCache) //nolint:errcheck

	const content = `{"type":"log","msg":"hello"}` + "\n" + `{"type":"log","msg":"world"}` + "\n"
	srcRun := makeSrcRun(t, content)

	// Switch to the receiver's cache before starting the transfer.
	os.Setenv("XDG_CACHE_HOME", receiverCache) //nolint:errcheck
	t.Cleanup(func() { os.Unsetenv("XDG_CACHE_HOME") }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	codeCh := make(chan string, 1)
	shareErrCh := make(chan error, 1)
	go func() {
		shareErrCh <- obwormhole.Share(ctx, srcRun, cfg, func(code string) {
			codeCh <- code
		})
	}()

	var code string
	select {
	case code = <-codeCh:
	case err := <-shareErrCh:
		t.Fatalf("share returned before sending code: %v", err)
	case <-ctx.Done():
		t.Fatal("timeout waiting for share code")
	}

	dstRun, err := obwormhole.Receive(ctx, code, cfg)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if err := <-shareErrCh; err != nil {
		t.Fatalf("share: %v", err)
	}

	got, err := os.ReadFile(dstRun.TelemetryPath())
	if err != nil {
		t.Fatalf("read received file: %v", err)
	}
	if string(got) != content {
		t.Errorf("content mismatch:\ngot:  %q\nwant: %q", got, content)
	}
	if dstRun.Lines != 2 {
		t.Errorf("lines: got %d, want 2", dstRun.Lines)
	}
}

func TestReceiveDuplicate(t *testing.T) {
	// The duplicate guard fires when you try to receive a run that is already
	// in YOUR cache — the primary scenario is a user running both `share` and
	// `receive` on the same machine (same cache dir).
	setCacheDir(t)
	cfg := newTestConfig(t)

	const content = `{"type":"log","msg":"dup"}` + "\n"
	srcRun := makeSrcRun(t, content)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	codeCh := make(chan string, 1)
	shareErrCh := make(chan error, 1)
	go func() {
		shareErrCh <- obwormhole.Share(ctx, srcRun, cfg, func(code string) { codeCh <- code })
	}()

	code := <-codeCh
	_, err := obwormhole.Receive(ctx, code, cfg)
	<-shareErrCh // drain; sender may see a rejection error

	var dupErr *obwormhole.DuplicateRunError
	if !isDuplicateRunError(err, &dupErr) {
		t.Fatalf("expected *DuplicateRunError, got: %v", err)
	}
	if dupErr.Run.ID != srcRun.ID {
		t.Errorf("duplicate run ID: got %s, want %s", dupErr.Run.ID, srcRun.ID)
	}
}

func isDuplicateRunError(err error, out **obwormhole.DuplicateRunError) bool {
	if err == nil {
		return false
	}
	v, ok := err.(*obwormhole.DuplicateRunError)
	if ok {
		*out = v
	}
	return ok
}

// ── local test relay ──────────────────────────────────────────────────────────

// startTestRelay starts a minimal wormhole transit relay on a random local
// port and returns its address. The listener is closed when the test ends.
func startTestRelay(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go serveRelay(l)
	return l.Addr().String()
}

// serveRelay implements the wormhole transit relay protocol.
// Each client sends "please relay <token> for side <sideID>\n".
// When two clients share the same token, the relay pairs and bridges them.
func serveRelay(l net.Listener) {
	var mu sync.Mutex
	waiting := make(map[string]net.Conn)

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			token, err := readRelayToken(conn)
			if err != nil {
				conn.Close()
				return
			}
			mu.Lock()
			other, exists := waiting[token]
			if exists {
				delete(waiting, token)
				mu.Unlock()
				conn.Write([]byte("ok\n"))  //nolint:errcheck
				other.Write([]byte("ok\n")) //nolint:errcheck
				relayBridge(conn, other)
			} else {
				waiting[token] = conn
				mu.Unlock()
			}
		}(conn)
	}
}

// readRelayToken reads "please relay <token> for side <sideID>\n" byte-by-byte
// so it never consumes bytes from the subsequent data stream.
func readRelayToken(conn net.Conn) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		if _, err := conn.Read(b); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			break
		}
		buf = append(buf, b[0])
	}
	// Expected: "please relay <token> for side <sideID>"
	parts := strings.Fields(string(buf))
	if len(parts) != 6 || parts[0] != "please" || parts[1] != "relay" {
		return "", fmt.Errorf("unexpected relay handshake: %q", string(buf))
	}
	return parts[2], nil
}

func relayBridge(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src) //nolint:errcheck
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}
