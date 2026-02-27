package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/btraven00/obadm/internal/aspire"
	"github.com/btraven00/obadm/internal/cache"
	"github.com/btraven00/obadm/internal/otlp"
	"github.com/btraven00/obadm/internal/sshconn"
)

// Set by -ldflags at build time.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const defaultAgentPath = "~/.obadm/bin/obadm-agent"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: obadm <command> [flags]")
		fmt.Fprintln(os.Stderr, "commands: stream, runs, replay, dashboard, version")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "stream":
		runStream(os.Args[2:])
	case "runs":
		runRuns(os.Args[2:])
	case "replay":
		runReplay(os.Args[2:])
	case "dashboard":
		runDashboard(os.Args[2:])
	case "version":
		fmt.Printf("obadm %s (commit %s, built %s)\n", version, commit, date)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	otlpAddr := fs.String("otlp", "localhost:4317", "local OTLP gRPC address to wait on")
	fs.Parse(args) //nolint:errcheck

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := aspire.EnsureRunning(ctx, *otlpAddr); err != nil {
		log.Fatalf("dashboard: %v", err)
	}
	log.Printf("dashboard: UI at http://localhost:18888")
}

func runStream(args []string) {
	fs := flag.NewFlagSet("stream", flag.ExitOnError)
	host := fs.String("host", "", "remote SSH host (required)")
	user := fs.String("user", "", "SSH user (default: ~/.ssh/config or $USER)")
	identity := fs.String("identity", "", "path to SSH private key (default: ~/.ssh/config IdentityFile)")
	remoteFile := fs.String("remote-file", "", "path to telemetry.jsonl on remote (required)")
	otlpAddr := fs.String("aspire", "localhost:4317", "local Aspire OTLP gRPC endpoint")
	agentPath := fs.String("agent-path", defaultAgentPath, "path to obadm-agent on remote")
	fs.Parse(args) //nolint:errcheck

	if *host == "" || *remoteFile == "" {
		fmt.Fprintln(os.Stderr, "error: --host and --remote-file are required")
		fs.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := aspire.EnsureRunning(ctx, *otlpAddr); err != nil {
		log.Fatalf("aspire: %v", err)
	}

	// Determine run: resume existing or start fresh.
	run, err := cache.Resume(*host, *remoteFile)
	var resumeLine int64
	if err != nil {
		run, err = cache.New(*host, *remoteFile)
		if err != nil {
			log.Fatalf("cache new: %v", err)
		}
		log.Printf("new run %s", run.ID)
	} else {
		resumeLine = run.Lines
		log.Printf("resuming run %s from line %d", run.ID, resumeLine)
	}

	cfg := sshconn.Config{
		Host:         *host,
		User:         *user,
		IdentityFile: *identity,
		RemoteFile:   *remoteFile,
		AgentPath:    *agentPath,
	}

	log.Printf("connecting to %s...", cfg.Host)
	conn, err := sshconn.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	// Send resume handshake before any reads.
	handshake := fmt.Sprintf(`{"resume_line":%d}`, resumeLine) + "\n"
	if _, err := fmt.Fprint(conn, handshake); err != nil {
		log.Fatalf("send resume handshake: %v", err)
	}

	// Open cache file for appending.
	cacheFile, err := run.Writer()
	if err != nil {
		log.Fatalf("cache writer: %v", err)
	}
	defer cacheFile.Close()

	lcw := &lineCountingWriter{w: cacheFile, lines: &run.Lines}
	tee := io.TeeReader(conn, lcw)

	// If resuming, replay cached lines to OTLP first.
	if resumeLine > 0 {
		cached, err := os.Open(run.TelemetryPath())
		if err != nil {
			log.Fatalf("open cache for replay: %v", err)
		}
		log.Printf("replaying %d cached lines to %s", resumeLine, *otlpAddr)
		if err := otlp.Forward(ctx, cached, *otlpAddr); err != nil {
			cached.Close()
			log.Fatalf("replay: %v", err)
		}
		cached.Close()
	}

	log.Printf("streaming %s → %s", *remoteFile, *otlpAddr)
	if err := otlp.Forward(ctx, tee, *otlpAddr); err != nil {
		log.Printf("stream ended: %v", err)
	}

	if err := run.Finish(); err != nil {
		log.Printf("finish run: %v", err)
	}
}

func runRuns(args []string) {
	if len(args) == 0 || args[0] == "list" {
		runs, err := cache.List()
		if err != nil {
			log.Fatalf("list runs: %v", err)
		}
		if len(runs) == 0 {
			fmt.Println("no runs")
			return
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RUN ID\tHOST\tSTARTED\tLINES\tSTATUS")
		for _, r := range runs {
			status := "in-progress"
			if r.FinishedAt != nil {
				status = "complete"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
				r.ID,
				r.Host,
				r.StartedAt.Format(time.DateTime),
				r.Lines,
				status,
			)
		}
		tw.Flush()
		return
	}
	fmt.Fprintf(os.Stderr, "unknown runs subcommand: %s\n", args[0])
	fmt.Fprintln(os.Stderr, "usage: obadm runs list")
	os.Exit(1)
}

func runReplay(args []string) {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	otlpAddr := fs.String("aspire", "localhost:4317", "local Aspire OTLP gRPC endpoint")
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: obadm replay [--aspire addr] <run-id>")
		os.Exit(1)
	}
	runID := fs.Arg(0)

	run, err := cache.Get(runID)
	if err != nil {
		log.Fatalf("get run: %v", err)
	}

	f, err := os.Open(run.TelemetryPath())
	if err != nil {
		log.Fatalf("open cache: %v", err)
	}
	defer f.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("replaying run %s (%d lines) → %s", run.ID, run.Lines, *otlpAddr)
	if err := otlp.Forward(ctx, f, *otlpAddr); err != nil {
		log.Fatalf("replay: %v", err)
	}
}

// lineCountingWriter wraps an io.Writer and increments *lines on each '\n'.
type lineCountingWriter struct {
	w     io.Writer
	lines *int64
}

func (l *lineCountingWriter) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	for i := 0; i < n; i++ {
		if p[i] == '\n' {
			*l.lines++
		}
	}
	return n, err
}
