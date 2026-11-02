# ESP Lab Bridge

An open-source local companion for the hosted ESP Lab. It starts the native `esp32sim`
engine and exposes the Tasmota web UI on loopback.

- listens only on `127.0.0.1:8788`
- starts `esp32sim` with `--net nat --hostfwd tcp:127.0.0.1:<port>-:80`
- downloads and verifies the Tasmota factory image and ESP32 mask ROM on first use
- serves a status page at <http://127.0.0.1:8788/>
- keeps one Tasmota session at a time
- no telemetry, auto-update, remote control channel or public listener

The source is intentionally small and uses only the Go standard library. See
[SECURITY.md](SECURITY.md) for the full security model.

## Run

Put `esp32sim` next to `esp-lab-bridge`, or set `ESP32SIM_BIN`:

```bash
ESP32SIM_BIN=/path/to/esp32sim ./esp-lab-bridge
```

Then open <http://127.0.0.1:8788/>.

The hosted app calls:

```text
GET  /health
POST /session
DELETE /session/:id
```

`POST /session` body:

```json
{
  "board": "kc868-a6",
  "flashMb": 4,
  "wifiSpec": "ssid=ESP-Lab,psk=esplab123",
  "script": "2.20 uart0 Backlog SSID1 ESP-Lab; Password1 esplab123\n..."
}
```

The response is:

```json
{
  "id": "…",
  "url": "http://127.0.0.1:41001/",
  "port": 41001
}
```

## Build

```bash
go test ./...
go build -trimpath -o esp-lab-bridge .
```

Or build all release targets:

```bash
./build.sh
```

The cross-compiled bridge binaries land in `dist/bridge/`. Build the Rust `esp32sim`
engine for each target platform separately and copy it beside the matching bridge binary.

For local development against the ESP Lab checkout:

```bash
go run . --engine ../ESP-Lab/engine/esp32sim/target/release/esp32sim \
  --assets ../ESP-Lab/.data/firmware
```

## CORS and private network access

The bridge allows the hosted origin and local development origins by default. Add more with:

```bash
./esp-lab-bridge --origin https://your-deployment.example.com
```

It answers Chrome's Private Network Access preflight with
`Access-Control-Allow-Private-Network: true`. It rejects non-loopback `Host` headers and
binds only to loopback.

## Firmware cache

Firmware is cached under `os.UserCacheDir()/esp-lab-bridge/firmware` and verified with
SHA-256 against pinned hashes. Use `--assets` to point at an existing directory:

```bash
./esp-lab-bridge --assets ../ESP-Lab/.data/firmware
```

## Releases

Tagged builds are published by GitHub Actions from this repository. Each archive contains
the bridge binary and a `SHA256SUMS` file. The bridge repository does not publish the
`esp32sim` engine itself; use the engine from the matching ESP Lab release or build it
from the ESP Lab source.

## Keep it small

No GUI, installer, auto-update, multi-session, proxy server, or WebSocket relay. The bridge
starts the native engine and gets out of the way.
