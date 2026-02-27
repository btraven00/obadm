# Persistent Agent Daemon

## Problem

The current design spawns a new `obadm-agent` process on every `obadm stream`
invocation via `ssh exec`. This creates a process-per-session model with two
failure modes:

1. **Process accumulation.** If the SSH session does not close cleanly (client
   crash, network drop), the agent process lingers on the remote host with no
   owner.

2. **Multiple agents tailing the same file.** The auto-resume flow in
   `obadm stream` reconnects frequently (crash recovery, manual restarts). Each
   reconnect spawns a new agent, potentially leaving the previous one still
   running.

## Proposed Design

Replace the ephemeral `ssh exec` model with a persistent daemon that follows the
`ssh-agent` socket pattern.

### Remote side

```
~/.obadm/
    bin/obadm-agent          # binary (unchanged deployment)
    run/
        agent.pid            # PID of running daemon
        agent.sock           # Unix domain socket
```

**Start sequence (first connect):**

1. Client SSHes in and runs: `obadm-agent --daemon --file <path>`
2. Agent checks for `agent.pid`. If the PID is alive and the socket exists,
   agent exits immediately (daemon already running).
3. Otherwise agent daemonizes: writes PID file, binds `agent.sock`, starts
   tailing the file.

**Connect sequence (subsequent connects):**

1. Client SSHes in and runs the same command.
2. Agent detects existing daemon, exits 0.
3. Client opens an SSH local forward to `agent.sock` (Unix socket forward,
   supported by OpenSSH and `golang.org/x/crypto/ssh`).
4. Client sends `{"resume_line": N}` over the forwarded connection.
5. Daemon streams from line N.

**Stop sequence:**

- Daemon exits when the file it is tailing is deleted or a `SIGTERM` is
  received.
- `obadm agent stop --host <host>` SSHes in and sends SIGTERM to the PID.

### Local side (`sshconn`)

`Connect` changes from `ssh.Session.Start(cmd)` + TCP forward to:

```
ssh exec: obadm-agent --daemon --file <path>   # idempotent start
ssh forward: unix → ~/.obadm/run/agent.sock    # single stable socket
```

The returned `io.ReadWriteCloser` is the forwarded Unix socket connection,
same interface as today.

### Protocol (unchanged)

```
client → {"resume_line": N}
daemon → JSONL stream from line N+1
```

## Trade-offs

| | Current (ephemeral) | Proposed (daemon) |
|---|---|---|
| Process count | 1 per stream session | 1 per file |
| Stale processes | possible on unclean exit | only if PID file stale |
| Agent restart required | never (new process each time) | on binary upgrade |
| SSH sessions per stream | 1 (exec + tunnel) | 1 (exec idempotent + socket forward) |
| Remote state | none | PID file + socket |

## PID file staleness

On start, the agent checks whether the PID in `agent.pid` is alive
(`kill(pid, 0)`). If the process is gone (stale PID file), it removes the file
and socket and starts fresh. This handles the host-reboot and OOM-kill cases.

## Out of scope

- Multiple files per daemon instance (one daemon per `--file` path).
- Agent auto-upgrade on connect (already handled separately via SFTP + SHA256).
