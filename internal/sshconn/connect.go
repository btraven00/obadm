package sshconn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	sshconfig "github.com/kevinburke/ssh_config"
	gossh "golang.org/x/crypto/ssh"
)

// Config holds parameters for connecting to a remote agent.
// Empty string fields fall back to ~/.ssh/config values for the given Host alias.
type Config struct {
	// Host is an SSH host alias or host:port address.
	Host string
	// User defaults to the value in ~/.ssh/config, then $USER.
	User string
	// IdentityFile defaults to the IdentityFile in ~/.ssh/config.
	IdentityFile string
	// RemoteFile is the absolute path to telemetry.jsonl on the remote host.
	RemoteFile string
	// AgentPath is the absolute path to obadm-agent on the remote host.
	AgentPath string
}

// resolveConfig fills in empty Config fields from ~/.ssh/config.
func resolveConfig(cfg *Config) {
	alias := cfg.Host // may be a bare alias like "myserver"

	if cfg.User == "" {
		if v := sshconfig.Get(alias, "User"); v != "" {
			cfg.User = v
		} else {
			cfg.User = os.Getenv("USER")
		}
	}

	if cfg.IdentityFile == "" {
		if v := sshconfig.Get(alias, "IdentityFile"); v != "" {
			cfg.IdentityFile = expandHome(v)
		}
	}

	// Resolve Hostname and Port for the actual TCP dial, leaving Host as the
	// alias so subsequent sshconfig lookups still work.
	hostname := sshconfig.Get(alias, "Hostname")
	if hostname == "" {
		hostname = alias
	}
	port := sshconfig.Get(alias, "Port")
	if port == "" {
		port = "22"
	}
	// Store resolved address back; strip any existing port first.
	bare, explicitPort, _ := strings.Cut(alias, ":")
	if explicitPort != "" {
		// Caller provided host:port explicitly — honour it.
		cfg.Host = bare + ":" + explicitPort
	} else {
		cfg.Host = hostname + ":" + port
	}
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// Conn is an active tunnel to a remote obadm-agent.
// It implements io.ReadCloser over the SSH-forwarded TCP connection.
type Conn struct {
	tunnel    net.Conn
	sshClient *gossh.Client
	session   *gossh.Session
}

func (c *Conn) Read(p []byte) (int, error)  { return c.tunnel.Read(p) }
func (c *Conn) Write(p []byte) (int, error) { return c.tunnel.Write(p) }

func (c *Conn) Close() error {
	c.tunnel.Close()
	c.session.Close()
	return c.sshClient.Close()
}

// Connect dials the remote SSH server, execs obadm-agent, sets up a local
// port forward, and returns a Conn whose Read yields raw JSONL lines.
// The caller must write the resume handshake ({"resume_line":N}) before reading.
// Empty fields in cfg are resolved from ~/.ssh/config before connecting.
func Connect(ctx context.Context, cfg Config) (io.ReadWriteCloser, error) {
	resolveConfig(&cfg)

	if cfg.IdentityFile == "" {
		return nil, fmt.Errorf("no identity file: set --identity or add IdentityFile to ~/.ssh/config for %q", cfg.Host)
	}

	keyBytes, err := os.ReadFile(cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("read identity file: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	sshCfg := &gossh.ClientConfig{
		User:            cfg.User,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), // TODO: use known_hosts
	}

	sshClient, err := gossh.Dial("tcp", cfg.Host, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", cfg.Host, err)
	}

	agentPath, err := ensureAgent(ctx, sshClient, cfg.AgentPath)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("ensure agent: %w", err)
	}

	session, err := sshClient.NewSession()
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		sshClient.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	cmd := agentPath + " --file " + shellQuote(cfg.RemoteFile)
	if err := session.Start(cmd); err != nil {
		session.Close()
		sshClient.Close()
		return nil, fmt.Errorf("start agent: %w", err)
	}

	var info struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(stdout).Decode(&info); err != nil {
		session.Close()
		sshClient.Close()
		return nil, fmt.Errorf("read agent port: %w", err)
	}
	if info.Port == 0 {
		session.Close()
		sshClient.Close()
		return nil, fmt.Errorf("agent reported port 0")
	}

	// Dial through the SSH tunnel to the agent's TCP listener.
	tunnel, err := sshClient.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", info.Port))
	if err != nil {
		session.Close()
		sshClient.Close()
		return nil, fmt.Errorf("tunnel to agent port %d: %w", info.Port, err)
	}

	// Close tunnel if context is cancelled.
	go func() {
		<-ctx.Done()
		tunnel.Close()
	}()

	return &Conn{tunnel: tunnel, sshClient: sshClient, session: session}, nil
}

// shellQuote wraps s in single quotes, escaping any existing single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
