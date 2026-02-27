package agent_test

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/btraven00/obadm/internal/agent"
)

func TestServe_StreamsExistingLines(t *testing.T) {
	f := writeTempJSONL(t, `{"event":"start","ts":1}`, `{"event":"data","ts":2}`)

	ln := mustListen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.Serve(ctx, f, ln) //nolint:errcheck

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	lines := readLines(t, conn, 2, 3*time.Second)
	want := []string{`{"event":"start","ts":1}`, `{"event":"data","ts":2}`}
	for i, got := range lines {
		if got != want[i] {
			t.Errorf("line %d: got %q want %q", i, got, want[i])
		}
	}
}

func TestServe_StreamsNewLines(t *testing.T) {
	f := writeTempJSONL(t, `{"event":"start","ts":1}`)

	ln := mustListen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.Serve(ctx, f, ln) //nolint:errcheck

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read the first line.
	_ = readLines(t, conn, 1, 3*time.Second)

	// Append a new line to the file.
	fh, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	fh.WriteString(`{"event":"end","ts":3}` + "\n") //nolint:errcheck
	fh.Close()

	lines := readLines(t, conn, 1, 3*time.Second)
	if lines[0] != `{"event":"end","ts":3}` {
		t.Errorf("appended line: got %q", lines[0])
	}
}

// writeTempJSONL creates a temp file with the given lines (each terminated with \n).
func writeTempJSONL(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	for _, l := range lines {
		f.WriteString(l + "\n") //nolint:errcheck
	}
	f.Close()
	return path
}

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// readLines reads exactly n lines from conn within timeout.
func readLines(t *testing.T, conn net.Conn, n int, timeout time.Duration) []string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck
	defer conn.SetDeadline(time.Time{})       //nolint:errcheck

	var lines []string
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) == n {
			return lines
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read lines: %v", err)
	}
	t.Fatalf("got %d lines, want %d", len(lines), n)
	return nil
}
