### `plugin-kasa` repository

#### Project Overview

This repository contains the `plugin-kasa`, a plugin that integrates the Slidebolt system with TP-Link Kasa smart home devices. It allows for local network discovery and control of Kasa smart plugs and bulbs.

#### Architecture

The `plugin-kasa` communicates with Kasa devices directly on the local network, requiring no cloud connection. It uses a combination of UDP for discovery and TCP for control.

-   **Local Discovery**: The plugin discovers Kasa devices by sending UDP broadcast probes on port 9999 and listening for responses. This allows it to automatically find new devices on the network and learn their IP addresses.

-   **Local Control**: Once a device is discovered, the plugin controls it by opening a direct TCP connection to the device on port 9999.

-   **Kasa Protocol**: The Kasa communication protocol uses JSON payloads that are obfuscated with a simple XOR stream cipher. This plugin implements the necessary encryption and decryption logic to communicate with the devices.

-   **Device Polling**: The plugin periodically polls the status of each known device to keep its state (e.g., on/off, brightness, color) synchronized with the Slidebolt system.

-   **Device Support**: It supports both simple smart plugs (represented as `switch` entities) and smart bulbs (represented as `light` entities), including those with color and temperature control. It can also handle multi-outlet power strips by creating a separate device for each outlet.

#### Key Files

| File | Description |
| :--- | :--- |
| `main.go` | The main entry point that initializes the plugin and starts the discovery and polling loops. |
| `kasa/client.go` | Implements the core client for communicating with Kasa devices, handling both UDP discovery and TCP-based command and control. |
| `kasa/crypto.go` | Contains the implementation of the XOR stream cipher used to encrypt and decrypt the Kasa communication protocol. |
| `kasa/types.go` | Defines the Go data structures that map to the JSON responses from the Kasa device API. |

#### Available Commands

The plugin translates and forwards standard Slidebolt commands to the appropriate Kasa devices. Supported commands include:

-   **`switch`**: `turn_on`, `turn_off`
-   **`light`**: `turn_on`, `turn_off`, `set_brightness`, `set_rgb`, `set_temperature`

#### Standalone Discovery Mode

This plugin supports a standalone discovery mode for rapid testing and diagnostics without requiring the full Slidebolt stack (NATS, Gateway, etc.).

To run discovery and output the results to JSON:
```bash
./plugin-kasa -discover
```

**Note**: Ensure any required environment variables (e.g., API keys, URLs) are set before running.
