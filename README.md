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
