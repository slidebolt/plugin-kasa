package logic

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// KasaClient is the interface through which the module communicates with
// TP-Link Kasa devices. It abstracts UDP discovery and TCP control so that
// tests can supply a mock implementation via ClientConstructor.
type KasaClient interface {
	SendUDPProbe() error
	ListenUDP(callback func(ip string, info KasaSysInfo)) (func(), error)
	SetPower(ip, childID string, state int) error
	SetLightState(ip string, params map[string]any) error
	GetSysInfo(ip string) (*KasaSysInfo, error)
	Close() error
}

// RealKasaClient is the production KasaClient that communicates with physical
// TP-Link Kasa devices over UDP (discovery) and TCP (control).
type RealKasaClient struct {
	udpConn *net.UDPConn
}

func (c *RealKasaClient) SendUDPProbe() error {
	probe := Encrypt(`{"system":{"get_sysinfo":null}}`)
	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: 9999}
	
	if c.udpConn != nil {
		fmt.Printf("[DEBUG] Kasa UDP Sending probe from shared listener %s to %s\n", c.udpConn.LocalAddr().String(), dest.String())
		_, err := c.udpConn.WriteToUDP(probe, dest)
		return err
	}

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil { return err }
	defer conn.Close()
	fmt.Printf("[DEBUG] Kasa UDP Sending probe from temporary port %s to %s\n", conn.LocalAddr().String(), dest.String())
	_, err = conn.WriteToUDP(probe, dest)
	return err
}

func (c *RealKasaClient) ListenUDP(callback func(ip string, info KasaSysInfo)) (func(), error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0} 
	conn, err := net.ListenUDP("udp", addr)
	if err != nil { return nil, err }
	c.udpConn = conn
	
	stop := func() { conn.Close() }

	go func() {
		fmt.Printf("[DEBUG] Kasa UDP Listener started on %s\n", conn.LocalAddr().String())
		buf := make([]byte, 4096)
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil { 
				return 
			}

			payload := Decrypt(buf[:n])
			var resp KasaResponse
			if err := json.Unmarshal([]byte(payload), &resp); err == nil && resp.System.SysInfo.Mac != "" {
				callback(remoteAddr.IP.String(), resp.System.SysInfo)
			}
		}
	}()
	return stop, nil
}

func (c *RealKasaClient) SetPower(ip, childID string, state int) error {
	cmd := fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, state)
	if childID != "" {
		cmd = fmt.Sprintf(`{"context":{"child_ids":["%s"]},"system":{"set_relay_state":{"state":%d}}}`, childID, state)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil { return err }
	defer conn.Close()

	payload := EncryptWithHeader(cmd)
	_, err = conn.Write(payload)
	return err
}

func (c *RealKasaClient) SetLightState(ip string, params map[string]any) error {
	inner, _ := json.Marshal(params)
	cmd := fmt.Sprintf(`{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":%s}}`, inner)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil { return err }
	defer conn.Close()

	payload := EncryptWithHeader(cmd)
	_, err = conn.Write(payload)
	return err
}

func (c *RealKasaClient) GetSysInfo(ip string) (*KasaSysInfo, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil { return nil, err }
	defer conn.Close()

	payload := EncryptWithHeader(`{"system":{"get_sysinfo":null}}`)
	_, err = conn.Write(payload)
	if err != nil { return nil, err }

	// Read response (Header + Payload)
	buf := make([]byte, 2048)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil { return nil, err }

	if n < 4 { return nil, fmt.Errorf("response too short") }
	data := Decrypt(buf[4:n])
	
	var resp KasaResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil { return nil, err }
	return &resp.System.SysInfo, nil
}
func (c *RealKasaClient) Close() error {
	if c.udpConn != nil { return c.udpConn.Close() }
	return nil
}

// ClientConstructor is a factory function that returns a KasaClient. Tests
// inject a mock implementation via the "_client_constructor" module config key.
type ClientConstructor func() KasaClient

// DefaultConstructor is the production ClientConstructor that creates a
// RealKasaClient with no pre-bound UDP connection.
var DefaultConstructor ClientConstructor = func() KasaClient {
	return &RealKasaClient{}
}