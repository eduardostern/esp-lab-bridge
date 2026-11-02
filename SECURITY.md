# Security

## Reporting

Please report a suspected vulnerability privately through GitHub's
[Security Advisories](https://github.com/eduardostern/esp-lab-bridge/security/advisories/new)
rather than in a public issue.

## What this program does

- listens only on `127.0.0.1:8788`
- serves a local status page
- starts the `esp32sim` binary that sits next to it (or is named by `ESP32SIM_BIN`)
- downloads two files from official GitHub releases:
  - Tasmota factory image
  - Espressif ESP32 mask ROM
- verifies both downloads with pinned SHA-256 hashes
- accepts browser requests only from an allowlisted origin
- rejects non-loopback `Host` headers
- stops the emulator when asked, when replaced, or after 10 minutes

## What this program does not do

- no telemetry
- no analytics
- no auto-update
- no remote control channel
- no listening on a LAN or public interface
- no shell commands from HTTP requests
- no project files or secrets sent to a server
- no Tasmota image or ROM bundled into the binary

## Build from source

The repository is intentionally small and dependency-free. A release binary should
match a local build:

```bash
git clone https://github.com/eduardostern/esp-lab-bridge
cd esp-lab-bridge
go test ./...
go build -trimpath -o esp-lab-bridge .
```

Compare checksums with the release assets if you want to verify a download.
