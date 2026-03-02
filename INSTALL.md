# Installing obadm

## Pre-built binaries (Linux and macOS, amd64/arm64)

```sh
curl -fsSL https://raw.githubusercontent.com/btraven00/obadm/main/install.sh | sh
```

Builds are published under the [`nightly`](https://github.com/btraven00/obadm/releases/tag/nightly) tag.

## Build from source

Requires Go 1.24+.

```sh
go build -o obadm ./cmd/obadm
go build -o obadm-agent ./cmd/obadm-agent
```
