package main

import (
	"encoding/json"
	"testing"

	"github.com/slidebolt/sdk-entities/light"
	"github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

type captureEvents struct{ last types.InboundEvent }

func (c *captureEvents) PublishEvent(evt types.InboundEvent) error {
	c.last = evt
	return nil
}

func TestEmitStateLightContract_TurnOn(t *testing.T) {
	cap := &captureEvents{}
	p := NewPluginKasaPlugin()
	p.pluginCtx = runner.PluginContext{Events: cap}

	state := light.State{Power: true, Brightness: 50}
	p.emitState("dev1", "light", true, &state)

	var payload map[string]any
	if err := json.Unmarshal(cap.last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Errorf("payload must not contain 'type' field (old format), got: %v", payload)
	}
	power, ok := payload["power"].(bool)
	if !ok {
		t.Fatalf("payload must contain 'power' bool field, got: %v", payload)
	}
	if !power {
		t.Errorf("expected power=true, got false")
	}
}

func TestEmitStateLightContract_TurnOff(t *testing.T) {
	cap := &captureEvents{}
	p := NewPluginKasaPlugin()
	p.pluginCtx = runner.PluginContext{Events: cap}

	state := light.State{Power: false}
	p.emitState("dev1", "light", false, &state)

	var payload map[string]any
	if err := json.Unmarshal(cap.last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Errorf("payload must not contain 'type' field (old format), got: %v", payload)
	}
	power, ok := payload["power"].(bool)
	if !ok {
		t.Fatalf("payload must contain 'power' bool field, got: %v", payload)
	}
	if power {
		t.Errorf("expected power=false, got true")
	}
}

func TestEmitStateLightContract_Brightness(t *testing.T) {
	cap := &captureEvents{}
	p := NewPluginKasaPlugin()
	p.pluginCtx = runner.PluginContext{Events: cap}

	state := light.State{Power: true, Brightness: 75}
	p.emitState("dev1", "light", true, &state)

	var payload map[string]any
	if err := json.Unmarshal(cap.last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Errorf("payload must not contain 'type' field, got: %v", payload)
	}
	if _, ok := payload["brightness"]; !ok {
		t.Errorf("payload must contain 'brightness' field, got: %v", payload)
	}
}

func TestEmitStateLightContract_RGB(t *testing.T) {
	cap := &captureEvents{}
	p := NewPluginKasaPlugin()
	p.pluginCtx = runner.PluginContext{Events: cap}

	rgb := []int{255, 0, 128}
	state := light.State{Power: true, RGB: rgb}
	p.emitState("dev1", "light", true, &state)

	var payload map[string]any
	if err := json.Unmarshal(cap.last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Errorf("payload must not contain 'type' field, got: %v", payload)
	}
	if _, ok := payload["rgb"]; !ok {
		t.Errorf("payload must contain 'rgb' field, got: %v", payload)
	}
}

func TestEmitStateLightContract_Temperature(t *testing.T) {
	cap := &captureEvents{}
	p := NewPluginKasaPlugin()
	p.pluginCtx = runner.PluginContext{Events: cap}

	state := light.State{Power: true, Temperature: 2700}
	p.emitState("dev1", "light", true, &state)

	var payload map[string]any
	if err := json.Unmarshal(cap.last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, hasType := payload["type"]; hasType {
		t.Errorf("payload must not contain 'type' field, got: %v", payload)
	}
	if _, ok := payload["temperature"]; !ok {
		t.Errorf("payload must contain 'temperature' field, got: %v", payload)
	}
}

func TestDiscoverDevices_IncludesCoreDevice(t *testing.T) {
	p := NewPluginKasaPlugin()
	p.macToIP = make(map[string]string)

	devices, err := p.discoverDevices()
	if err != nil {
		t.Fatalf("discoverDevices failed: %v", err)
	}

	coreID := types.CoreDeviceID("plugin-kasa")
	found := false
	for _, d := range devices {
		if d.ID == coreID {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected core device %q in discoverDevices result", coreID)
	}
}
