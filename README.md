# Kasa Device API Reference

This is a minimal reference implementation showing how to connect to TP-Link Kasa smart devices.

## Connection Overview

**Discovery**: UDP broadcast on port 9999  
**Control**: TCP connections to port 9999 on each device  
**Protocol**: JSON commands wrapped in XOR stream cipher encryption

## Quick Start

```bash
# Set your subnet
echo "KASA_SUBNET=192.168.88" > .env.local
source .env.local

# Run discovery
go run .
```

## Local device tests

The plugin includes integration tests for real Kasa hardware. They load
`KASA_*` variables from `plugin-kasa/.env.local` when present, and you can also
pass overrides inline.

```bash
# Non-mutating: discovery + entity registration for a specific strip
KASA_SUBNET=192.168.88 \
KASA_TEST_DEVICE_IP=192.168.88.38 \
go test -tags integration -run 'TestIntegration_TargetStripRegistersNamedOutlets|TestDiscovery_RegistersEP40OutletsAsEntities' ./cmd/plugin-kasa
```

```bash
# Mutating: flips one outlet, verifies the state change, then restores it
KASA_SUBNET=192.168.88 \
KASA_TEST_DEVICE_IP=192.168.88.38 \
KASA_TEST_OUTLET_NAME='deck holiday lights' \
KASA_TEST_ALLOW_MUTATION=1 \
go test -tags integration -run TestIntegration_TargetStripOutletRoundTrip ./cmd/plugin-kasa
```

Optional overrides:

- `KASA_TEST_DEVICE_ID` to assert the discovered device ID
- `KASA_TEST_EXPECTED_NAMES` as a comma-separated outlet name list
- `KASA_TEST_OUTLET_NAME` to choose which outlet the live round-trip test toggles
- `KASA_TEST_ALLOW_MUTATION=1` to enable the live toggle test

## API Commands

### Get System Info
```json
{"system":{"get_sysinfo":null}}
```

### Turn On/Off
```json
{"system":{"set_relay_state":{"state":1}}}  // on
{"system":{"set_relay_state":{"state":0}}}  // off
```

Multi-outlet devices such as the EP40 expose multiple switch entities under one
device. Per-outlet control uses the same relay command with a Kasa context:

```json
{
  "context": {"child_ids": ["<outlet-id>"]},
  "system": {"set_relay_state": {"state": 1}}
}
```

### Set Light Brightness
```json
{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":{"brightness":100,"on_off":1}}}
```

### Set Color Temperature
```json
{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":{"color_temp":3000,"on_off":1}}}
```

### Set HSV Color
```json
{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":{"hue":120,"saturation":100,"brightness":100,"on_off":1}}}
```

## Encryption

Kasa uses a simple XOR stream cipher:
- Start key: 171
- Each byte: `encrypted = plaintext ^ key`
- Next key = encrypted byte

TCP frames have a 4-byte big-endian length header before the encrypted payload.

## Environment Variables

```bash
KASA_SUBNET=192.168.88        # Subnet to scan (required)
KASA_TIMEOUT_MS=400           # Discovery timeout per device
KASA_DISCOVERY_CONCURRENCY=64 # Parallel scan workers
```
