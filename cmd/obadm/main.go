package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/btraven00/obadm/internal/aspire"
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
		fmt.Fprintln(os.Stderr, "commands: stream, dashboard, version")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "stream":
		runStream(os.Args[2:])
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

	cfg := sshconn.Config{
		Host:         *host,
		User:         *user,
		IdentityFile: *identity,
		RemoteFile:   *remoteFile,
		AgentPath:    *agentPath,
	}

	log.Printf("connecting to %s@%s...", cfg.User, cfg.Host)
	conn, err := sshconn.Connect(ctx, cfg)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	log.Printf("streaming %s → %s", *remoteFile, *otlpAddr)
	if err := otlp.Forward(ctx, conn, *otlpAddr); err != nil {
		log.Fatalf("forward: %v", err)
	}
}
