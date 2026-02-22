# Kasa Plugin for Slidebolt

The Kasa Plugin provides seamless integration for TP-Link Kasa smart home devices (plugs, bulbs, and switches). It features automatic network discovery and real-time control through the Slidebolt Framework.

## Features

- **Automatic Discovery**: Scans the local network for Kasa devices using UDP broadcast on port 9999.
- **Protocol Support**: Implements the Kasa XOR-encrypted JSON protocol.
- **Device Support**: Compatible with most Kasa smart plugs, wall switches, and bulbs.
- **Real-time Updates**: Pushes state changes to the Slidebolt Core via NATS.

## Architecture

This plugin follows the Slidebolt "Isolated Service" pattern:
- **`pkg/bundle`**: Implementation of the `sdk.Plugin` interface.
- **`pkg/logic`**: Handles the Kasa protocol, encryption, and device communication.
- **`cmd/main.go`**: Service entry point.

## Development

### Prerequisites
- Go (v1.25.6+)
- Slidebolt `plugin-sdk` and `plugin-framework` repos sitting as siblings.

### Local Build
Initialize the Go workspace to link sibling dependencies:
```bash
go work init . ../plugin-sdk ../plugin-framework
go build -o bin/plugin-kasa ./cmd/main.go
```

### Testing
- **Unit Tests**: `go test ./...`
- **Hardware Tests**: Located in `tests/hardware/` (Git-ignored). To run them, ensure you have a physical device at the IP specified in the test files.

## Docker Deployment

### Deployment Requirements
**CRITICAL**: This plugin requires **Host Networking** (`network_mode: host`) to perform UDP broadcast discovery of physical devices on your network.

### Build the Image
To build with local sibling modules:
```bash
make docker-build-local
```

### Run via Docker Compose
Add the following to your `docker-compose.yml`:
```yaml
services:
  kasa:
    image: slidebolt-plugin-kasa:latest
    network_mode: "host"
    environment:
      - NATS_URL=nats://127.0.0.1:24232 # Point to your Core's host-mapped port
    restart: always
```

## License
Refer to the root project license.
