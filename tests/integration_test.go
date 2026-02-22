package tests

import (
	"testing"
	"time"

	 "github.com/slidebolt/plugin-kasa/pkg/logic"
)

func TestKasaPlugRegistersAsActuator(t *testing.T) {
	mock := &MockKasaClient{}
	api := newTestAPI("kasa-bundle", map[string]any{
		"_client_constructor": logic.ClientConstructor(func() logic.KasaClient {
			return mock
		}),
	})

	handler := logic.NewModuleHandler()
	if err := handler.Init(api); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer handler.Stop()

	mock.TriggerDiscovery("127.0.0.1", logic.KasaSysInfo{
		Mac:        "AABBCCDDEEFF",
		Alias:      "Living Room Plug",
		Model:      "HS103(US)",
		DevType:    "IOT.SMARTPLUGSWITCH",
		RelayState: 1,
	})

	inst := waitForDevice(t, api, "aabbccddeeff")

	if len(inst.Entities) != 1 {
		t.Fatalf("Expected 1 entity, got %d", len(inst.Entities))
	}
	if inst.Entities[0].ID != "power" || inst.Entities[0].Kind != "actuator" {
		t.Fatalf("Expected actuator 'power', got %+v", inst.Entities[0])
	}
	if inst.EntityState["power"]["power"] != true {
		t.Errorf("Expected power=true (relay_state=1), got %v", inst.EntityState["power"]["power"])
	}

	t.Log("✅ Kasa plug correctly registered as actuator")
}

func TestKasaBulbRegistersAsLight(t *testing.T) {
	mock := &MockKasaClient{}
	api := newTestAPI("kasa-bundle", map[string]any{
		"_client_constructor": logic.ClientConstructor(func() logic.KasaClient {
			return mock
		}),
	})

	handler := logic.NewModuleHandler()
	if err := handler.Init(api); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer handler.Stop()

	colorTemp := 4000
	mock.TriggerDiscovery("127.0.0.1", logic.KasaSysInfo{
		Mac:     "FFEEDDCCBBAA",
		Alias:   "Bedroom Bulb",
		Model:   "KL130(US)",
		DevType: "IOT.SMARTBULB",
		LightState: &logic.KasaLightState{
			OnOff:      1,
			Brightness: 75,
			ColorTemp:  &colorTemp,
		},
	})

	inst := waitForDevice(t, api, "ffeeddccbbaa")

	if len(inst.Entities) != 1 {
		t.Fatalf("Expected 1 entity, got %d", len(inst.Entities))
	}
	entity := inst.Entities[0]
	if entity.ID != "light" || entity.Kind != "actuator" {
		t.Fatalf("Expected actuator 'light', got %+v", entity)
	}
	if _, ok := entity.Capabilities["brightness"]; !ok {
		t.Fatalf("Expected brightness capability, got %+v", entity.Capabilities)
	}
	if _, ok := entity.Capabilities["color_temp"]; !ok {
		t.Fatalf("Expected color_temp capability, got %+v", entity.Capabilities)
	}

	state := inst.EntityState["light"]
	if state["power"] != true {
		t.Errorf("Expected power=true, got %v", state["power"])
	}
	if state["brightness"] != 75 {
		t.Errorf("Expected brightness=75, got %v", state["brightness"])
	}
	if state["color_temp"] != 4000 {
		t.Errorf("Expected color_temp=4000, got %v", state["color_temp"])
	}

	t.Log("✅ Kasa bulb correctly registered as light with brightness and color temp")
}

func TestKasaCommandForwarding(t *testing.T) {
	mock := &MockKasaClient{}
	api := newTestAPI("kasa-bundle", map[string]any{
		"_client_constructor": logic.ClientConstructor(func() logic.KasaClient {
			return mock
		}),
	})

	handler := logic.NewModuleHandler()
	if err := handler.Init(api); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer handler.Stop()

	mock.TriggerDiscovery("127.0.0.1", logic.KasaSysInfo{
		Mac:   "AABBCCDDEEFF",
		Alias: "Test Plug",
		Model: "HS103",
	})
	time.Sleep(500 * time.Millisecond)

	gotState := -1
	mock.OnSetPower = func(state int) { gotState = state }

	api.Send("commands/aabbccddeeff", "power", map[string]any{"power": true})
	time.Sleep(500 * time.Millisecond)

	if gotState != 1 {
		t.Fatalf("Expected SetPower(1), got %d", gotState)
	}
	t.Log("✅ Power command forwarded correctly")
}
