package main

import (
	"fmt"
	"net"
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

func decrypt(b []byte) string {
	key := byte(171)
	result := make([]byte, len(b))
	for i := 0; i < len(b); i++ {
		result[i] = b[i] ^ key
		key = b[i]
	}
	return string(result)
}

func main() {
	// Kasa Discovery JSON
	discoveryMsg := `{"system":{"get_sysinfo":null}}`
	payload := encrypt(discoveryMsg)

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: 9999}
	fmt.Println("Broadcasting discovery probe to 255.255.255.255:9999...")
	_, err = conn.WriteToUDP(payload, dest)
	if err != nil {
		panic(err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)

	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("Discovery finished.")
			break
		}
		
		decrypted := decrypt(buf[:n])
		fmt.Printf("\n[Device Found] %s\nPayload: %s\n", addr.IP, decrypted)
	}
}
