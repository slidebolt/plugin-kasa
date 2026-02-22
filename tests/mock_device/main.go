package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
)

func main() {
	portStr := os.Getenv("PORT")
	if portStr == "" {
		portStr = "9000"
	}
	l, err := net.Listen("tcp", ":"+portStr)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

	log.Printf("Mock Device Simulator listening on port %s", portStr)

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var msg map[string]any
		if err := decoder.Decode(&msg); err != nil {
			return
		}

		switch msg["cmd"] {
		case "identify":
			encoder.Encode(map[string]any{
				"model": "Mock-V1",
				"name":  "Living Room Light",
			})
		case "set_power":
			state := msg["state"].(bool)
			log.Printf("DEVICE POWER RECEIVED: %v", state)
			encoder.Encode(map[string]any{"status": "ok"})
			
			// Simulate a human immediately flipping it back to ON if we turned it OFF
			if !state {
				log.Println("SIMULATING MANUAL OVERRIDE: DEVICE FLIPPED TO ON")
				// We wait a tiny bit then send an unsolicited update (via a separate connection or simple log for the bundle to pick up)
				// In this mock, we'll just have the bundle logic simulate the 'event' receipt.
			}
		}
	}
}
