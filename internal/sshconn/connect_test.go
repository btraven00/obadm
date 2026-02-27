package sshconn_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"

	"github.com/btraven00/obadm/internal/agent"
	"github.com/btraven00/obadm/internal/sshconn"
)

// TestConnect_StreamsViaSSH starts an in-process SSH server whose handler calls
// agent.Serve() directly (no real binary). It verifies that sshconn.Connect
// returns a reader that streams JSONL lines through the SSH tunnel.
func TestConnect_StreamsViaSSH(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "telemetry.jsonl")
	if err := os.WriteFile(filePath, []byte(`{"event":"start","ts":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a dummy agent binary so ensureAgent finds it via SFTP stat.
	agentPath := filepath.Join(dir, "obadm-agent")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Generate ephemeral client key pair.
	clientPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientSigner, err := gossh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatalf("client signer: %v", err)
	}

	keyPath := filepath.Join(dir, "id_rsa")
	if err := writePKCS1Key(keyPath, clientPriv); err != nil {
		t.Fatalf("write key: %v", err)
	}

	srvAddr := startTestSSHServer(t, clientSigner.PublicKey(), filePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := sshconn.Connect(ctx, sshconn.Config{
		Host:         srvAddr,
		User:         "testuser",
		IdentityFile: keyPath,
		RemoteFile:   filePath,
		AgentPath:    agentPath, // absolute path, no ~ expansion needed
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	type deadliner interface{ SetDeadline(time.Time) error }
	if dl, ok := conn.(deadliner); ok {
		dl.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("expected a line, got none: %v", scanner.Err())
	}
	got := scanner.Text()
	want := `{"event":"start","ts":1}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// startTestSSHServer starts a gliderlabs SSH server with:
//   - SFTP subsystem backed by the real OS filesystem (for ensureAgent stat)
//   - direct-tcpip forwarding (for the SSH tunnel)
//   - session handler that runs agent.Serve() in-process
func startTestSSHServer(t *testing.T, clientPub gossh.PublicKey, filePath string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &gssh.Server{
		PublicKeyHandler: func(_ gssh.Context, key gssh.PublicKey) bool {
			return gssh.KeysEqual(key, clientPub)
		},
		LocalPortForwardingCallback: func(_ gssh.Context, _ string, _ uint32) bool {
			return true
		},
		ChannelHandlers: map[string]gssh.ChannelHandler{
			"session":      gssh.DefaultSessionHandler,
			"direct-tcpip": gssh.DirectTCPIPHandler,
		},
		SubsystemHandlers: map[string]gssh.SubsystemHandler{
			"sftp": func(s gssh.Session) {
				srv, err := sftp.NewServer(s)
				if err != nil {
					return
				}
				srv.Serve() //nolint:errcheck
			},
		},
		Handler: func(s gssh.Session) {
			agentLn, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				fmt.Fprintf(s.Stderr(), "agent listen: %v\n", err)
				s.Exit(1)
				return
			}
			port := agentLn.Addr().(*net.TCPAddr).Port
			if err := json.NewEncoder(s).Encode(struct {
				Port int `json:"port"`
			}{port}); err != nil {
				s.Exit(1)
				return
			}
			agent.Serve(s.Context(), filePath, agentLn) //nolint:errcheck
			s.Exit(0)
		},
	}

	t.Cleanup(func() {
		srv.Close()
		ln.Close()
	})
	go srv.Serve(ln) //nolint:errcheck

	return ln.Addr().String()
}

// writePKCS1Key writes an RSA private key in PKCS#1 PEM format.
func writePKCS1Key(path string, key *rsa.PrivateKey) error {
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}
