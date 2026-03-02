package wormhole

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/btraven00/obadm/internal/cache"
	ww "github.com/psanford/wormhole-william/wormhole"
)

// Config holds optional server overrides.
// Zero value uses wormhole-william's default public servers.
type Config struct {
	// RendezvousURL overrides the default rendezvous WebSocket URL.
	RendezvousURL string
	// TransitRelayAddress overrides the default transit relay host:port.
	TransitRelayAddress string
}

func newClient(cfg Config) ww.Client {
	return ww.Client{
		RendezvousURL:       cfg.RendezvousURL,
		TransitRelayAddress: cfg.TransitRelayAddress,
	}
}

// Share sends run's telemetry file to a wormhole receiver.
// onCode is called with the wormhole code once the sender is ready.
// Share blocks until the transfer completes or ctx is cancelled.
func Share(ctx context.Context, run *cache.Run, cfg Config, onCode func(string)) error {
	f, err := os.Open(run.TelemetryPath())
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	c := newClient(cfg)
	code, status, err := c.SendFile(ctx, run.ID+".jsonl", f)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	if onCode != nil {
		onCode(code)
	}
	s := <-status
	if s.Error != nil {
		return fmt.Errorf("transfer: %w", s.Error)
	}
	return nil
}

// DuplicateRunError is returned by Receive when the sender's run ID is
// already present in the local cache.
type DuplicateRunError struct {
	Run *cache.Run
}

func (e *DuplicateRunError) Error() string {
	return fmt.Sprintf("run %s already in cache", e.Run.ID)
}

// Receive downloads a telemetry run via wormhole code and stores it in the
// cache. Returns the new Run on success. Returns *DuplicateRunError if the
// run is already cached.
func Receive(ctx context.Context, code string, cfg Config) (*cache.Run, error) {
	c := newClient(cfg)
	msg, err := c.Receive(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("receive: %w", err)
	}

	// If the sender embedded a run ID in the filename (<uuid>.jsonl), check
	// for a duplicate before creating a new cache entry.
	if srcID := strings.TrimSuffix(msg.Name, ".jsonl"); srcID != msg.Name {
		if existing, err := cache.Get(srcID); err == nil {
			msg.Reject() //nolint:errcheck // tell sender we're not accepting
			return nil, &DuplicateRunError{Run: existing}
		}
	}

	run, err := cache.New("shared", code)
	if err != nil {
		return nil, fmt.Errorf("new run: %w", err)
	}

	w, err := run.Writer()
	if err != nil {
		run.Remove() //nolint:errcheck
		return nil, fmt.Errorf("open writer: %w", err)
	}

	log.Printf("receiving %s (%d bytes) → run %s", msg.Name, msg.TransferBytes64, run.ID[:8])
	lcw := &lineCountWriter{w: w, lines: &run.Lines}
	_, copyErr := io.Copy(lcw, msg)
	w.Close()
	if copyErr != nil {
		run.Remove() //nolint:errcheck
		return nil, fmt.Errorf("copy: %w", copyErr)
	}

	if err := run.Finish(); err != nil {
		return run, fmt.Errorf("finish: %w", err)
	}
	return run, nil
}

type lineCountWriter struct {
	w     io.Writer
	lines *int64
}

func (l *lineCountWriter) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	for i := 0; i < n; i++ {
		if p[i] == '\n' {
			*l.lines++
		}
	}
	return n, err
}
