package main

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/slidebolt/plugin-kasa/kasa"
	entityswitch "github.com/slidebolt/sdk-entities/switch"
	"github.com/slidebolt/sdk-types"
)

// MockKasaDevice simulates a Kasa device for testing.
type MockKasaDevice struct {
	IP         string
	MAC        string
	Model      string
	RelayState int
	IsBulb     bool
	LightState *kasa.KasaLightState
}

func (m *MockKasaDevice) ServeTCP() {
	l, err := net.Listen("tcp", m.IP+":9999")
	if err != nil {
		return
	}
	defer l.Close()
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			buf := make([]byte, 2048)
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			if n < 4 {
				return
			}
			data := kasa.Decrypt(buf[4:n])

			var resp any
			if data == `{"system":{"get_sysinfo":null}}` {
				sysInfo := kasa.KasaSysInfo{
					Alias:      "Mock Device",
					Model:      m.Model,
					Mac:        m.MAC,
					RelayState: m.RelayState,
				}
				if m.IsBulb {
					sysInfo.DevType = "IOT.SMARTBULB"
					sysInfo.LightState = m.LightState
				}

				r := kasa.KasaResponse{}
				r.System.SysInfo = sysInfo
				resp = r
			} else if data == `{"system":{"set_relay_state":{"state":1}}}` || data == `{"system":{"set_relay_state":{"state":0}}}` {
				m.RelayState = 0
				if data[len(data)-3] == '1' {
					m.RelayState = 1
				}
				r := kasa.KasaResponse{}
				r.System.SetRelayState.ErrCode = 0
				resp = r
			}

			if resp != nil {
				resData, _ := json.Marshal(resp)
				c.Write(kasa.EncryptWithHeader(string(resData)))
			}
		}(conn)
	}
}

func TestPluginLogic(t *testing.T) {
	if normalizeMac("AA:BB:CC:DD:EE:FF") != "aabbccddeeff" {
		t.Errorf("Mac normalization failed")
	}

	h, s, v := rgbToHsv(255, 0, 0)
	if h != 0 || s != 100 || v != 100 {
		t.Errorf("RGB to HSV failed for red: %d, %d, %d", h, s, v)
	}

	h, s, v = rgbToHsv(0, 255, 0)
	if h != 120 || s != 100 || v != 100 {
		t.Errorf("RGB to HSV failed for green: %d, %d, %d", h, s, v)
	}
}

func TestHandleCommand(t *testing.T) {
	// Placeholder — tested via TestOnCommand with a mock client.
}

type MockKasaClient struct {
	SetPowerCalled bool
	LastState      int
}

func (m *MockKasaClient) SendUDPProbe() error { return nil }
func (m *MockKasaClient) ListenUDP(callback func(ip string, info kasa.KasaSysInfo)) (func(), error) {
	return func() {}, nil
}
func (m *MockKasaClient) SetPower(ip, childID string, state int) error {
	m.SetPowerCalled = true
	m.LastState = state
	return nil
}
func (m *MockKasaClient) SetLightState(ip string, params map[string]any) error { return nil }
func (m *MockKasaClient) GetSysInfo(ip string) (*kasa.KasaSysInfo, error)      { return nil, nil }
func (m *MockKasaClient) Close() error                                         { return nil }

func TestOnCommand(t *testing.T) {
	mockClient := &MockKasaClient{}

	p := NewPluginKasaPlugin()
	p.client = mockClient
	p.macToIP = map[string]string{"dev1": "127.0.0.1"}

	ent := types.Entity{
		DeviceID: "dev1",
		Domain:   entityswitch.Type,
	}
	req := types.Command{
		ID:       "cmd-1",
		DeviceID: "dev1",
		EntityID: "ent-1",
		Payload:  json.RawMessage(`{"type":"turn_on"}`),
	}

	err := p.OnCommand(req, ent)
	if err != nil {
		t.Fatalf("OnCommand failed: %v", err)
	}

	if !mockClient.SetPowerCalled {
		t.Errorf("SetPower was not called")
	}
	if mockClient.LastState != 1 {
		t.Errorf("Expected state 1, got %d", mockClient.LastState)
	}
}
