package kasa

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// Client is the interface through which the module communicates with
// TP-Link Kasa devices. It abstracts UDP discovery and TCP control.
type Client interface {
	SendUDPProbe() error
	ListenUDP(callback func(ip string, info KasaSysInfo)) (func(), error)
	SetPower(ip, childID string, state int) error
	SetLightState(ip string, params map[string]any) error
	GetSysInfo(ip string) (*KasaSysInfo, error)
	Close() error
}

// RealClient is the production Client that communicates with physical
// TP-Link Kasa devices over UDP (discovery) and TCP (control).
type RealClient struct {
	udpConn *net.UDPConn
}

func (c *RealClient) SendUDPProbe() error {
	probe := Encrypt(`{"system":{"get_sysinfo":null}}`)
	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: 9999}

	if c.udpConn != nil {
		_, err := c.udpConn.WriteToUDP(probe, dest)
		return err
	}

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.WriteToUDP(probe, dest)
	return err
}

func (c *RealClient) ListenUDP(callback func(ip string, info KasaSysInfo)) (func(), error) {
	// Kasa devices broadcast on port 9999, but we listen on a random port for their replies
	// actually the probe is sent to 9999, and they reply to the source port.
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	c.udpConn = conn

	stop := func() { conn.Close() }

	go func() {
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

func (c *RealClient) SetPower(ip, childID string, state int) error {
	cmd := fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, state)
	if childID != "" {
		cmd = fmt.Sprintf(`{"context":{"child_ids":["%s"]},"system":{"set_relay_state":{"state":%d}}}`, childID, state)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload := EncryptWithHeader(cmd)
	_, err = conn.Write(payload)
	return err
}

func (c *RealClient) SetLightState(ip string, params map[string]any) error {
	inner, _ := json.Marshal(params)
	cmd := fmt.Sprintf(`{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":%s}}`, inner)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload := EncryptWithHeader(cmd)
	_, err = conn.Write(payload)
	return err
}

func (c *RealClient) GetSysInfo(ip string) (*KasaSysInfo, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := EncryptWithHeader(`{"system":{"get_sysinfo":null}}`)
	_, err = conn.Write(payload)
	if err != nil {
		return nil, err
	}

	// Read response (Header: 4 bytes length)
	header := make([]byte, 4)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > 16384 {
		return nil, fmt.Errorf("response too large: %d", length)
	}

	dataBuf := make([]byte, length)
	if _, err := io.ReadFull(conn, dataBuf); err != nil {
		return nil, err
	}

	data := Decrypt(dataBuf)
	var resp KasaResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}
	return &resp.System.SysInfo, nil
}

func (c *RealClient) Close() error {
	if c.udpConn != nil {
		return c.udpConn.Close()
	}
	return nil
}

func NewRealClient() *RealClient {
	return &RealClient{}
}
