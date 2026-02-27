package aspire

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"
)

const (
	containerName = "aspire"
	image         = "mcr.microsoft.com/dotnet/aspire-dashboard:latest"
	readyTimeout  = 30 * time.Second
)

// EnsureRunning starts the Aspire dashboard container if it is not already
// running. It tries docker first, then podman. It waits until the OTLP gRPC
// port at otlpAddr is accepting connections before returning.
func EnsureRunning(ctx context.Context, otlpAddr string) error {
	runtime, err := findRuntime()
	if err != nil {
		return err
	}

	if isRunning(ctx, runtime) {
		log.Printf("aspire: dashboard already running")
		return nil
	}

	log.Printf("aspire: starting dashboard container via %s...", runtime)
	if err := startDetached(ctx, runtime); err != nil {
		return fmt.Errorf("start aspire container: %w", err)
	}

	return waitReady(ctx, otlpAddr, readyTimeout)
}

// findRuntime returns the first available container runtime (docker or podman).
func findRuntime() (string, error) {
	for _, candidate := range []string{"docker", "podman"} {
		if path, err := exec.LookPath(candidate); err == nil {
			_ = path
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no container runtime found: install docker or podman")
}

func isRunning(ctx context.Context, runtime string) bool {
	out, err := exec.CommandContext(ctx, runtime, "ps",
		"--filter", "name=^/"+containerName+"$",
		"--format", "{{.Names}}",
	).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == containerName
}

func startDetached(ctx context.Context, runtime string) error {
	cmd := exec.CommandContext(ctx, runtime, "run",
		"--detach",
		"--rm",
		"--name", containerName,
		"-p", "18888:18888", // dashboard UI
		"-p", "18891:18891", // MCP endpoint
		"-p", "4317:18889",  // OTLP gRPC: host 4317 → container 18889
		"-e", "ASPIRE_DASHBOARD_UNSECURED_ALLOW_ANONYMOUS=true",
		"-e", "ASPIRE_ALLOW_UNSECURED_TRANSPORT=true",
		"-e", "ASPIRE_DASHBOARD_MCP_ENDPOINT_URL=http://0.0.0.0:18891",
		"-e", "Dashboard__Mcp__AuthMode=Unsecured",
		"-e", "Dashboard__Frontend__EndpointUrls=http://localhost:18888",
		"-e", "Dashboard__Frontend__PublicUrl=http://localhost:18888",
		image,
	)
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	log.Printf("aspire: container started (%s)", strings.TrimSpace(string(out)))
	return nil
}

func waitReady(ctx context.Context, addr string, timeout time.Duration) error {
	log.Printf("aspire: waiting for OTLP endpoint at %s...", addr)
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("aspire not ready at %s after %s", addr, timeout)
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			log.Printf("aspire: ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
