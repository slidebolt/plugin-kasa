package logic

import (
	"fmt"

	"github.com/lms-io/module-framework/pkg/framework"
)

// RawEntitiesFromSysInfo derives bundle-native raw entities from Kasa sysinfo.
func RawEntitiesFromSysInfo(info KasaSysInfo, childID string) []framework.RawEntitySpec {
	if childID != "" {
		return []framework.RawEntitySpec{rawPowerEntity(childID, info.Alias)}
	}

	if info.IsLight() {
		raw := []framework.RawEntitySpec{rawPowerEntity("power", "Power")}
		raw = append(raw, rawBrightnessEntity())
		if supportsColorTemp(info) {
			raw = append(raw, rawColorTempEntity())
		}
		if supportsColor(info) {
			raw = append(raw, rawColorEntity())
		}
		return raw
	}

	return []framework.RawEntitySpec{rawPowerEntity("power", "Main Power")}
}

// EntitiesFromRaw maps raw entities to abstract entities used by the framework/UI.
func EntitiesFromRaw(raw []framework.RawEntitySpec, info KasaSysInfo, childID string) []framework.EntitySpec {
	if childID != "" {
		return []framework.EntitySpec{toggleEntity("power", info.Alias, rawLink("kasa.relay", "power"))}
	}

	if info.IsLight() {
		caps := map[string]any{
			"toggle":     true,
			"brightness": map[string]any{"min": 0, "max": 100, "step": 1},
		}
		links := []string{rawLink("kasa.relay", "power"), rawLink("kasa.brightness", "brightness")}
		if supportsColorTemp(info) {
			caps["color_temp"] = map[string]any{"min": 2500, "max": 9000, "step": 100}
			links = append(links, rawLink("kasa.color_temp", "color_temp"))
		}
		if supportsColor(info) {
			caps["color"] = true
			links = append(links, rawLink("kasa.color", "color"))
		}
		return []framework.EntitySpec{{
			ID:           "light",
			Kind:         "actuator",
			Name:         info.Alias,
			Capabilities: caps,
			Links:        links,
		}}
	}

	return []framework.EntitySpec{toggleEntity("power", "Main Power", rawLink("kasa.relay", "power"))}
}

// RawStateFromSysInfo captures bundle-native state keyed by raw entity IDs.
func RawStateFromSysInfo(info KasaSysInfo, childID string) map[string]map[string]any {
	state := map[string]map[string]any{}
	if childID != "" {
		state["power"] = map[string]any{"power": info.ChildPower(childID)}
		return state
	}

	if info.IsLight() {
		on := info.RelayState == 1
		if info.LightState != nil {
			on = info.LightState.OnOff == 1
			state["brightness"] = map[string]any{"value": info.LightState.Brightness}
			if supportsColorTemp(info) {
				state["color_temp"] = map[string]any{"value": intValue(info.LightState.ColorTemp)}
			}
			if supportsColor(info) {
				state["color"] = map[string]any{
					"hue":        intValue(info.LightState.Hue),
					"saturation": intValue(info.LightState.Saturation),
				}
			}
		}
		state["power"] = map[string]any{"power": on}
		return state
	}

	state["power"] = map[string]any{"power": info.RelayState == 1}
	return state
}

// EntityStateFromSysInfo captures abstract state keyed by entity IDs.
func EntityStateFromSysInfo(info KasaSysInfo, childID string) map[string]map[string]any {
	state := map[string]map[string]any{}
	if childID != "" {
		state["power"] = map[string]any{"power": info.ChildPower(childID)}
		return state
	}

	if info.IsLight() {
		entry := map[string]any{}
		if info.LightState != nil {
			entry["power"] = info.LightState.OnOff == 1
			entry["brightness"] = info.LightState.Brightness
			if supportsColorTemp(info) {
				entry["color_temp"] = intValue(info.LightState.ColorTemp)
			}
			if supportsColor(info) {
				entry["hue"] = intValue(info.LightState.Hue)
				entry["saturation"] = intValue(info.LightState.Saturation)
			}
		} else {
			entry["power"] = info.RelayState == 1
		}
		state["light"] = entry
		return state
	}

	state["power"] = map[string]any{"power": info.RelayState == 1}
	return state
}

func rawPowerEntity(id, name string) framework.RawEntitySpec {
	return framework.RawEntitySpec{ID: id, Kind: "kasa.relay", Name: name, Raw: map[string]any{"id": id}}
}

func rawBrightnessEntity() framework.RawEntitySpec {
	return framework.RawEntitySpec{ID: "brightness", Kind: "kasa.brightness", Name: "Brightness", Raw: map[string]any{"min": 0, "max": 100, "step": 1}}
}

func rawColorTempEntity() framework.RawEntitySpec {
	return framework.RawEntitySpec{ID: "color_temp", Kind: "kasa.color_temp", Name: "Color Temp", Raw: map[string]any{"min": 2500, "max": 9000, "step": 100, "unit": "K"}}
}

func rawColorEntity() framework.RawEntitySpec {
	return framework.RawEntitySpec{ID: "color", Kind: "kasa.color", Name: "Color", Raw: map[string]any{"model": "hsl"}}
}

func toggleEntity(id, name, link string) framework.EntitySpec {
	return framework.EntitySpec{
		ID:           id,
		Kind:         "actuator",
		Name:         name,
		Capabilities: map[string]any{"toggle": true},
		Links:        []string{link},
	}
}

func rawLink(kind, id string) string {
	return fmt.Sprintf("raw:%s:%s", kind, id)
}

func supportsColorTemp(info KasaSysInfo) bool {
	return info.LightState != nil && info.LightState.ColorTemp != nil
}

func supportsColor(info KasaSysInfo) bool {
	return info.LightState != nil && (info.LightState.Hue != nil || info.LightState.Saturation != nil)
}

func intValue(val *int) int {
	if val == nil {
		return 0
	}
	return *val
}
