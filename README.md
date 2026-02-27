# obadm

Admin tools for nicer benchmarking.

## Quick start

```sh
go build -o obadm ./cmd/obadm
go build -o obadm-agent ./cmd/obadm-agent
```

With a host configured in `~/.ssh/config`:

```sh
./obadm stream --host myserver --remote-file /data/omnibenchmark/telemetry.jsonl
```

This will start the Aspire dashboard if not already running, deploy the agent to the remote host if needed, and stream telemetry to `http://localhost:18888`.

---

## Commands

### `obadm dashboard`

Starts the [Aspire](https://learn.microsoft.com/en-us/dotnet/aspire/fundamentals/dashboard/overview) OTel dashboard container if it is not already running, then exits.

```sh
./obadm dashboard
```

| Flag | Default | Description |
|---|---|---|
| `--otlp` | `localhost:4317` | OTLP gRPC address to wait on before returning |

Dashboard UI is available at `http://localhost:18888` once running.

---

### `obadm stream`

Streams `telemetry.jsonl` from a remote host to the local Aspire dashboard via OTLP gRPC over an SSH tunnel. Starts Aspire automatically if not running. Deploys `obadm-agent` to the remote host if not present.

With a host alias configured in `~/.ssh/config`, the verbose flags are unnecessary:

```sh
./obadm stream --host myserver --remote-file /data/omnibenchmark/telemetry.jsonl
```

Otherwise, pass them explicitly:

```sh
./obadm stream \
  --host remote.example.com \
  --user alice \
  --identity ~/.ssh/id_ed25519 \
  --remote-file /data/omnibenchmark/telemetry.jsonl
```

`--user`, `--identity`, hostname, and port are resolved from `~/.ssh/config` when not provided.

| Flag | Default | Description |
|---|---|---|
| `--host` | | Remote SSH host or `~/.ssh/config` alias (required) |
| `--user` | `~/.ssh/config` / `$USER` | SSH user |
| `--identity` | `~/.ssh/config` | Path to SSH private key |
| `--remote-file` | | Path to `telemetry.jsonl` on remote (required) |
| `--aspire` | `localhost:4317` | Local Aspire OTLP gRPC endpoint |
| `--agent-path` | `~/.obadm/bin/obadm-agent` | Path to `obadm-agent` on remote |
