package device

import (
	"os"
	"github.com/slidebolt/plugin-framework"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
	"github.com/slidebolt/plugin-sdk"
	"testing"
	"time"
)

type testKasaClient struct {
	info            logic.KasaSysInfo
	lastPowerState  int
	lastLightParams map[string]any
}

func (m *testKasaClient) SendUDPProbe() error { return nil }
func (m *testKasaClient) ListenUDP(callback func(ip string, info logic.KasaSysInfo)) (func(), error) {
	return func() {}, nil
}
func (m *testKasaClient) SetPower(ip, childID string, state int) error {
	m.lastPowerState = state
	if m.info.LightState != nil {
		m.info.LightState.OnOff = state
	}
	m.info.RelayState = state
	return nil
}
func (m *testKasaClient) SetLightState(ip string, params map[string]any) error {
	m.lastLightParams = params
	if m.info.LightState == nil {
		m.info.LightState = &logic.KasaLightState{}
	}
	if v, ok := params["brightness"].(int); ok {
		m.info.LightState.Brightness = v
	}
	if v, ok := params["color_temp"].(int); ok {
		m.info.LightState.ColorTemp = &v
	}
	m.info.LightState.OnOff = 1
	m.info.RelayState = 1
	return nil
}
func (m *testKasaClient) GetSysInfo(ip string) (*logic.KasaSysInfo, error) {
	return &m.info, nil
}
func (m *testKasaClient) Close() error { return nil }

func TestRegisterAndCommand_KasaPublishesState(t *testing.T) {
	_ = os.RemoveAll("state")
	_ = os.RemoveAll("logs")
	t.Cleanup(func() {
		_ = os.RemoveAll("state")
		_ = os.RemoveAll("logs")
	})

	b, err := framework.RegisterBundle("plugin-kasa-adapter-test")
	if err != nil {
		t.Fatalf("register bundle: %v", err)
	}

	temp := 3200
	client := &testKasaClient{info: logic.KasaSysInfo{
		Alias:      "Desk Lamp",
		Model:      "KL125",
		MicType:    "IOT.SMARTBULB",
		Mac:        "AA:BB:CC",
		RelayState: 0,
		LightState: &logic.KasaLightState{OnOff: 0, Brightness: 10, ColorTemp: &temp},
	}}

	Register(b, client, "192.168.1.44", client.info)
	time.Sleep(150 * time.Millisecond)

	devObj, ok := b.GetBySourceID(sdk.SourceID("aabbcc"))
	if !ok {
		t.Fatalf("expected kasa device")
	}
	dev := devObj.(sdk.Device)
	ents, _ := dev.GetEntities()
	if len(ents) == 0 {
		t.Fatalf("expected main entity")
	}
	light := ents[0].(sdk.Light)

	_ = light.SetBrightness(55)
	time.Sleep(150 * time.Millisecond)
	if got, _ := client.lastLightParams["brightness"].(int); got != 55 {
		t.Fatalf("expected brightness command, got %+v", client.lastLightParams)
	}

	if light.State().Status != "active" {
		t.Fatalf("expected light active after brightness command, got %s", light.State().Status)
	}
}
