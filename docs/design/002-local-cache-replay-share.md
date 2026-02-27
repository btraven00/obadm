# Local Cache, Replay, and Share — Design Spec

## Motivation

`obadm stream` sits in the middle of the telemetry pipeline. At zero extra cost it
can tee the raw JSONL to a local file, turning every run into a durable artifact
keyed by its global span ID. This unlocks replay, offline analysis, and
peer-to-peer sharing without any server-side infrastructure.

---

## Run Cache

### Layout

Root via `os.UserCacheDir()` — `~/.cache` on Linux, `~/Library/Caches` on macOS.

```
$XDG_CACHE_HOME/obadm/runs/
  <run-id>/
    telemetry.jsonl   # raw line-delimited events as received from agent
    meta.json         # run metadata
```

`<run-id>` is a UUID generated at session start. Span ID extraction from the
first JSONL line (field name TBD) can be added later as an alias — the UUID
remains the canonical key so the cache requires zero knowledge of the data
structure.

### `meta.json`

```json
{
  "span_id":    "abc123",
  "host":       "remote.example.com",
  "remote_file": "/data/omnibenchmark/telemetry.jsonl",
  "started_at": "2026-02-27T00:00:00Z",
  "finished_at": null,
  "lines":      14823
}
```

`finished_at` is null while streaming is in progress (crash-safe sentinel).

### Stream tee

`obadm stream` wraps the SSH tunnel reader in an `io.TeeReader` before passing
it to `otlp.Forward`:

```
agent → SSH tunnel → io.TeeReader ──→ local cache file
                          └─────────→ otlp.Forward → Aspire
```

No new packages needed. The run UUID is generated with `github.com/google/uuid`
(already a transitive dep). Span ID extraction is deferred — field name unknown
at spike time.

---

## Reconnect / Broken Streams

The resume cursor is the number of lines successfully written to the local cache
— not bytes received, not records forwarded to OTLP. This guarantees line
integrity across disconnects (the agent only emits complete `\n`-terminated lines;
the cache only counts lines fully written to disk).

On reconnect:

```
resume_line = wc -l $XDG_CACHE_HOME/obadm/runs/<run-id>/telemetry.jsonl
→ client sends {"resume_line": N} to agent before streaming begins
→ agent seeks to line N+1 (bufio.Scanner skip loop)
→ local cache file reopened O_APPEND
→ OTLP replay of cached-but-not-forwarded lines triggered if Aspire was down
```

### Test strategy (no real network drops needed)

```go
// simulate disconnect after 3 lines
brokenReader := io.LimitReader(tunnelConn, bytesOfFirst3Lines)
// write partial cache, then reconnect and verify resume from line 3
```

The in-process SSH server already supports this pattern — inject the limit in the
test, verify the resumed stream contains exactly the remaining lines with no
duplicates or gaps.

---

## New Commands

### `obadm runs list`

```
SPAN ID     HOST                STARTED              LINES    STATUS
abc123      remote.example.com  2026-02-27 00:00:00  14823    complete
def456      remote.example.com  2026-02-28 00:00:00  3021     in-progress
```

### `obadm replay <span-id>`

Reads `~/.obadm/runs/<span-id>/telemetry.jsonl` and re-emits to an OTLP
endpoint. Reuses `otlp.Forward(ctx, file, addr)` unchanged — the only
difference from `stream` is the source is a local `*os.File` instead of
an SSH tunnel.

Flags:
- `--aspire` (default `localhost:4317`) — target OTLP endpoint
- `--from-line N` — skip first N lines (partial replay)
- `--speed 2.0` — replay at Nx wall-clock speed (optional stretch goal)

### `obadm share <span-id>`

Transfers `~/.obadm/runs/<span-id>/telemetry.jsonl` to a peer using the
**Magic Wormhole** protocol (wormhole-william, the pure-Go implementation).

#### Why Magic Wormhole

- No server to operate — rendezvous via a public relay, data is E2E encrypted
- Human-pronounceable code (`7-crossword-clockwork`) — easy to share over chat
- Pure Go: `github.com/psanford/wormhole-william`
- Receiver needs only `obadm receive` (or the standard `wormhole` CLI)

#### Flow

```
Sender:
  obadm share abc123
  → compresses run dir to tar.gz in memory
  → initiates wormhole transfer
  → prints: "wormhole code: 7-crossword-clockwork"

Receiver:
  obadm receive 7-crossword-clockwork
  → downloads + decompresses into ~/.obadm/runs/<span-id>/
  → ready for obadm replay
```

#### Implementation sketch

```go
import "github.com/psanford/wormhole-william/wormhole"

c := wormhole.Client{}
code, status, err := c.SendFile(ctx, "telemetry.jsonl", file)
fmt.Printf("wormhole code: %s\n", code)
<-status  // wait for transfer completion
```

Interoperates with the Python `magic-wormhole` CLI — receiver doesn't need
obadm installed.

---

## Non-Goals

- Cloud storage / centralised run repository (out of scope)
- Streaming share (share while run is in progress) — possible future extension
- Encryption of local cache (OS filesystem permissions are sufficient for now)

---

## Implementation Order

1. Local cache tee in `obadm stream` (prerequisite for everything else)
2. `obadm runs list` (trivial once cache exists)
3. `obadm replay` (reuses `otlp.Forward` unchanged)
4. `obadm share` / `obadm receive` (self-contained, no coupling to stream)
