package device

import (
	"fmt"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
	"github.com/slidebolt/plugin-sdk"
	"strings"
	"sync"
)

type kasaAdapter struct {
	bundle  sdk.Bundle
	client  logic.KasaClient
	ip      string
	device  sdk.Device
	entity  sdk.Entity
	mac     string
	started bool
	mu      sync.Mutex
}

var (
	adaptersMu sync.Mutex
	adapters   = map[string]*kasaAdapter{}
)

func Register(b sdk.Bundle, client logic.KasaClient, ip string, info logic.KasaSysInfo) {
	sid := normalizeMAC(info.Mac)
	if sid == "" {
		return
	}

	adaptersMu.Lock()
	defer adaptersMu.Unlock()

	key := fmt.Sprintf("%s/%s", b.ID(), sid)
	if a, ok := adapters[key]; ok {
		a.mu.Lock()
		a.ip = ip
		a.mu.Unlock()
		a.refreshStateFromSysInfo(info)
		return
	}

	var dev sdk.Device
	if obj, ok := b.GetBySourceID(sdk.SourceID(sid)); ok {
		dev = obj.(sdk.Device)
	} else {
		created, err := b.CreateDevice()
		if err != nil {
			return
		}
		dev = created
		name := strings.TrimSpace(info.Alias)
		if name == "" {
			name = sid
		}
		_ = dev.UpdateMetadata(name, sdk.SourceID(sid))
	}
	_ = dev.UpdateRaw(map[string]interface{}{
		"ip":       ip,
		"model":    info.Model,
		"mic_type": info.MicType,
		"mac":      sid,
	})

	var ent sdk.Entity
	if ents, err := dev.GetEntities(); err == nil && len(ents) > 0 {
		ent = ents[0]
	} else {
		if info.IsLight() {
			caps := []string{sdk.CAP_BRIGHTNESS}
			if info.IsTemp() {
				caps = append(caps, sdk.CAP_TEMPERATURE)
			}
			if info.IsColor() {
				caps = append(caps, sdk.CAP_RGB)
			}
			created, err := dev.CreateEntityEx(sdk.TYPE_LIGHT, caps)
			if err != nil {
				return
			}
			ent = created
		} else {
			created, err := dev.CreateEntity(sdk.TYPE_SWITCH)
			if err != nil {
				return
			}
			ent = created
		}
		_ = ent.UpdateMetadata("Main", sdk.SourceID(fmt.Sprintf("%s-main", sid)))
	}

	a := &kasaAdapter{bundle: b, client: client, ip: ip, device: dev, entity: ent, mac: sid}
	adapters[key] = a
	a.bindCommands()
	a.refreshStateFromSysInfo(info)
	a.startPolling()

	b.Log().Info("Registered Kasa %s at %s (%s)", info.Alias, ip, sid)
}

func (a *kasaAdapter) bindCommands() {
	handler := func(cmd string, payload map[string]interface{}) {
		a.mu.Lock()
		ip := a.ip
		a.mu.Unlock()

		state := map[string]interface{}{}
		switch cmd {
		case "TurnOn", "ToggleOn":
			if err := a.client.SetPower(ip, "", 1); err != nil {
				a.bundle.Log().Error("Kasa TurnOn failed [%s]: %v", a.mac, err)
				return
			}
			state["power"] = true
		case "TurnOff", "ToggleOff":
			if err := a.client.SetPower(ip, "", 0); err != nil {
				a.bundle.Log().Error("Kasa TurnOff failed [%s]: %v", a.mac, err)
				return
			}
			state["power"] = false
		case "SetBrightness":
			if val, ok := toInt(payload["level"]); ok {
				if err := a.client.SetLightState(ip, map[string]any{"brightness": val, "on_off": 1}); err != nil {
					a.bundle.Log().Error("Kasa SetBrightness failed [%s]: %v", a.mac, err)
					return
				}
				state["power"] = true
				state["brightness"] = val
			}
		case "SetTemperature":
			if val, ok := toInt(payload["kelvin"]); ok {
				if err := a.client.SetLightState(ip, map[string]any{"color_temp": val, "on_off": 1}); err != nil {
					a.bundle.Log().Error("Kasa SetTemperature failed [%s]: %v", a.mac, err)
					return
				}
				state["power"] = true
				state["kelvin"] = val
				state["temperature"] = val
			}
		case "SetRGB":
			r, rok := toInt(payload["r"])
			g, gok := toInt(payload["g"])
			b, bok := toInt(payload["b"])
			if !rok || !gok || !bok {
				break
			}
			hsl := sdk.RGBToHSL(r, g, b)
			if err := a.client.SetLightState(ip, map[string]any{"hue": hsl.H, "saturation": hsl.S, "on_off": 1}); err != nil {
				a.bundle.Log().Error("Kasa SetRGB/HS failed [%s]: %v", a.mac, err)
				return
			}
			state["power"] = true
			state["hue"] = hsl.H
			state["saturation"] = hsl.S
		}

		if len(state) == 0 {
			return
		}

		a.publishState(state)
		a.refreshState()
	}

	a.device.OnCommand(handler)
	a.entity.OnCommand(handler)
}

func (a *kasaAdapter) startPolling() {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return
	}
	a.started = true
	a.mu.Unlock()

	a.bundle.Every15Seconds(func() {
		a.refreshState()
	})
}

func (a *kasaAdapter) refreshState() {
	a.mu.Lock()
	ip := a.ip
	a.mu.Unlock()
	info, err := a.client.GetSysInfo(ip)
	if err != nil || info == nil {
		a.bundle.Log().Debug("Kasa get_sysinfo failed [%s]: %v", a.mac, err)
		return
	}
	a.refreshStateFromSysInfo(*info)
}

func (a *kasaAdapter) refreshStateFromSysInfo(info logic.KasaSysInfo) {
	state := map[string]interface{}{}
	if info.IsLight() && info.LightState != nil {
		state["power"] = info.LightState.OnOff == 1
		state["brightness"] = info.LightState.Brightness
		if info.LightState.ColorTemp != nil {
			state["kelvin"] = *info.LightState.ColorTemp
			state["temperature"] = *info.LightState.ColorTemp
		}
		if info.LightState.Hue != nil {
			state["hue"] = *info.LightState.Hue
		}
		if info.LightState.Saturation != nil {
			state["saturation"] = *info.LightState.Saturation
		}
	} else {
		state["power"] = info.RelayState == 1
	}
	state["ip"] = a.ip
	a.publishState(state)
}

func (a *kasaAdapter) publishState(state map[string]interface{}) {
	power, _ := state["power"].(bool)

	if !hasCustomScript(a.entity) {
		if power {
			_ = a.entity.UpdateState("active")
		} else {
			_ = a.entity.UpdateState("active") // Device is still active framework-wise
		}
	}
	// power field is already in state
	_ = a.entity.Publish(fmt.Sprintf("entity.%s.state", a.entity.ID()), state)
}

func DiscoverStatic(_ sdk.Bundle, _ logic.KasaClient) {
	// Intentionally disabled: Kasa discovery is broadcast-based.
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	default:
		return 0, false
	}
}

func normalizeMAC(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, ":", "")
	v = strings.ReplaceAll(v, "-", "")
	return v
}

func hasCustomScript(ent sdk.Entity) bool {
	s := strings.TrimSpace(ent.Script())
	return s != "" && s != "-- OnLoad() {}"
}
