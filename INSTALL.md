# Installing obadm

## Pre-built binaries (Linux and macOS, amd64/arm64)

```sh
curl -fsSL "https://github.com/btraven00/obadm/releases/latest/download/obadm_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" -o obadm && chmod +x obadm
```

Nightly builds are also available under the `nightly` tag on the [releases page](https://github.com/btraven00/obadm/releases).

## Build from source

Requires Go 1.24+.

```sh
go build -o obadm ./cmd/obadm
go build -o obadm-agent ./cmd/obadm-agent
```
