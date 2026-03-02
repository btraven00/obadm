# obadm

Admin tools for nicer benchmarking.

## Install

```sh
curl -fsSL "https://github.com/btraven00/obadm/releases/latest/download/obadm_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" -o obadm && chmod +x obadm
```

See [INSTALL.md](INSTALL.md) for other installation methods.

## Quick start

With a host configured in `~/.ssh/config`:

```sh
obadm stream myserver:/data/omnibenchmark/telemetry.jsonl
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

```sh
./obadm stream [user@]host:path [flags]
```

Examples:

```sh
# host alias from ~/.ssh/config
./obadm stream myserver:/data/omnibenchmark/telemetry.jsonl

# explicit user and host
./obadm stream alice@remote.example.com:/data/omnibenchmark/telemetry.jsonl

# with a non-default SSH key
./obadm stream myserver:/data/omnibenchmark/telemetry.jsonl --identity ~/.ssh/id_ed25519
```

`user`, hostname, port, and identity file are resolved from `~/.ssh/config` when not provided.

| Flag | Default | Description |
|---|---|---|
| `--identity` | `~/.ssh/config` | Path to SSH private key |
| `--aspire` | `localhost:4317` | Local Aspire OTLP gRPC endpoint |
| `--agent-path` | `~/.obadm/bin/obadm-agent` | Path to `obadm-agent` on remote |
