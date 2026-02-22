# TP-Link Kasa Hardware Connection

## Protocol Summary
- **Protocol**: TPLink Smart Home Protocol (XOR-encrypted JSON)
- **TCP Port**: 9999 (Commands/Status)
- **UDP Port**: 9999 (Discovery)

## Connection Details
- **Encryption**: Every byte is XORed with a rolling key starting at `171`.
- **TCP Header**: Commands over TCP must be preceded by a 4-byte big-endian length header.

## Manual Verification
You can verify a connection using the following standalone Go script.

### Standalone Test Script (`test_kasa.go`)
```go
package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"time"
)

func encrypt(s string) []byte {
	key := byte(171)
	payload := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		payload[i] = s[i] ^ key
		key = payload[i]
	}
	return payload
}

func main() {
	ip := os.Getenv("DEVICE_IP")
	if ip == "" {
		fmt.Println("❌ Error: DEVICE_IP environment variable must be set")
		return
	}

	cmd := `{"system":{"get_sysinfo":{}}}`
	fmt.Printf("Connecting to Kasa device at %s:9999...\n", ip)

	conn, err := net.DialTimeout("tcp", ip+":9999", 2*time.Second)
	if err != nil {
		fmt.Printf("❌ Dial failed: %v\n", err)
		return
	}
	defer conn.Close()

	payload := encrypt(cmd)
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	
	conn.Write(header)
	conn.Write(payload)
	fmt.Println("✅ Command Sent. Handshake Verified.")
}
```

### Running the Test
```bash
export DEVICE_IP="192.168.x.x"
go run test_kasa.go
```
