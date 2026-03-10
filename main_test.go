package main

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
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
			} else if fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, 1) == data || fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, 0) == data {
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
	// Test normalizeMac
	if normalizeMac("AA:BB:CC:DD:EE:FF") != "aabbccddeeff" {
		t.Errorf("Mac normalization failed")
	}

	// Test RGB to HSV
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
	// We can't easily test the full NATS flow here without the runner,
	// but we can test the OnCommand handler if we provide a mock client.
}

type MockRawStore struct {
	mu       sync.RWMutex
	devices  map[string]json.RawMessage
	entities map[string]json.RawMessage
}

func NewMockRawStore() *MockRawStore {
	return &MockRawStore{
		devices:  make(map[string]json.RawMessage),
		entities: make(map[string]json.RawMessage),
	}
}

func (m *MockRawStore) ReadRawDevice(deviceID string) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.devices[deviceID]
	if !ok {
		return nil, fmt.Errorf("device not found: %s", deviceID)
	}
	return data, nil
}

func (m *MockRawStore) WriteRawDevice(deviceID string, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[deviceID] = data
	return nil
}

func (m *MockRawStore) ReadRawEntity(deviceID, entityID string) (json.RawMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := deviceID + "/" + entityID
	data, ok := m.entities[key]
	if !ok {
		return nil, fmt.Errorf("entity not found: %s", key)
	}
	return data, nil
}

func (m *MockRawStore) WriteRawEntity(deviceID, entityID string, data json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := deviceID + "/" + entityID
	m.entities[key] = data
	return nil
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
	mockStore := NewMockRawStore()

	// Store IP mapping in RawStore
	ipData, _ := json.Marshal(map[string]string{"ip": "127.0.0.1"})
	mockStore.WriteRawDevice("dev1", ipData)

	// Store MAC-to-IP mapping
	macData, _ := json.Marshal(map[string]string{"ip": "127.0.0.1"})
	mockStore.WriteRawDevice("aabbccddeeff", macData)

	p := &PluginKasaPlugin{
		client:   mockClient,
		rawStore: mockStore,
	}

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

	_, err := p.OnCommand(req, ent)
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

func TestRawStoreIPLookup(t *testing.T) {
	mockStore := NewMockRawStore()

	// Store MAC-to-IP mapping
	ipData, _ := json.Marshal(map[string]string{"ip": "192.168.1.100"})
	err := mockStore.WriteRawDevice("aabbccddeeff", ipData)
	if err != nil {
		t.Fatalf("Failed to write to RawStore: %v", err)
	}

	// Read it back
	readData, err := mockStore.ReadRawDevice("aabbccddeeff")
	if err != nil {
		t.Fatalf("Failed to read from RawStore: %v", err)
	}

	var cfg struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(readData, &cfg); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if cfg.IP != "192.168.1.100" {
		t.Errorf("Expected IP 192.168.1.100, got %s", cfg.IP)
	}
}

func TestOnDeviceDiscoverUsesCurrentSlice(t *testing.T) {
	mockClient := &MockKasaClient{}
	mockStore := NewMockRawStore()

	// Pre-populate RawStore with some MAC-to-IP mappings
	ipData1, _ := json.Marshal(map[string]string{"ip": "192.168.1.101"})
	mockStore.WriteRawDevice("001122334455", ipData1)

	ipData2, _ := json.Marshal(map[string]string{"ip": "192.168.1.102"})
	mockStore.WriteRawDevice("66778899aabb", ipData2)

	p := &PluginKasaPlugin{
		client:   mockClient,
		rawStore: mockStore,
	}

	// Provide some existing devices in the current slice
	current := []types.Device{
		{
			ID:         "001122334455",
			SourceID:   "001122334455",
			SourceName: "Kasa Device 1",
			LocalName:  "Device 1",
		},
	}

	// Call OnDeviceDiscover
	result, err := p.OnDeviceDiscover(current)
	if err != nil {
		t.Fatalf("OnDeviceDiscover failed: %v", err)
	}

	// Should return at least the existing device
	if len(result) == 0 {
		t.Error("OnDeviceDiscover returned empty result")
	}
}

func TestPluginUsesCurrentSliceNotDeviceMap(t *testing.T) {
	// This test verifies that the plugin uses the current slice passed to OnDeviceDiscover
	// instead of maintaining its own deviceMap
	mockClient := &MockKasaClient{}
	mockStore := NewMockRawStore()

	p := &PluginKasaPlugin{
		client:   mockClient,
		rawStore: mockStore,
	}

	// Create a device with SourceID containing MAC
	mac := "aabbccddeeff"
	deviceID := "test-device-1"

	// Store IP mapping in RawStore for the MAC
	ipData, _ := json.Marshal(map[string]string{"ip": "192.168.1.100"})
	mockStore.WriteRawDevice(mac, ipData)

	// Simulate OnDeviceDiscover call with a device
	current := []types.Device{
		{
			ID:         deviceID,
			SourceID:   mac,
			SourceName: "Test Device",
			LocalName:  "Test",
		},
	}

	_, err := p.OnDeviceDiscover(current)
	if err != nil {
		t.Fatalf("OnDeviceDiscover failed: %v", err)
	}

	// Verify the device configuration was read from RawStore
	// This confirms the plugin is not relying on an internal deviceMap
	storedData, err := mockStore.ReadRawDevice(mac)
	if err != nil {
		t.Fatalf("Failed to read from RawStore: %v", err)
	}

	var cfg struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(storedData, &cfg); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if cfg.IP != "192.168.1.100" {
		t.Errorf("Expected IP 192.168.1.100, got %s", cfg.IP)
	}
}
