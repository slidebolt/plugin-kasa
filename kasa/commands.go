package kasa

import (
	"encoding/json"
	"fmt"
)

// CommandBuilder creates Kasa protocol commands from high-level actions
type CommandBuilder struct{}

// NewCommandBuilder creates a new CommandBuilder instance
func NewCommandBuilder() *CommandBuilder {
	return &CommandBuilder{}
}

// SetRelayState creates a command to set relay state (on/off)
func (cb *CommandBuilder) SetRelayState(childID string, state int) string {
	cmd := fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, state)
	if childID != "" {
		cmd = fmt.Sprintf(`{"context":{"child_ids":["%s"]},"system":{"set_relay_state":{"state":%d}}}`, childID, state)
	}
	return cmd
}

// GetSysInfo creates a command to get system info
func (cb *CommandBuilder) GetSysInfo() string {
	return `{"system":{"get_sysinfo":null}}`
}

// TransitionLightState creates a command for light state transitions
func (cb *CommandBuilder) TransitionLightState(params map[string]any) string {
	inner, _ := json.Marshal(params)
	return fmt.Sprintf(`{"smartlife.iot.smartbulb.lightingservice":{"transition_light_state":%s}}`, inner)
}

// LightParams creates light parameter map for brightness
func (cb *CommandBuilder) LightParamsBrightness(brightness int, onOff int) map[string]any {
	return map[string]any{
		"brightness": brightness,
		"on_off":     onOff,
	}
}

// LightParams creates light parameter map for color temperature
func (cb *CommandBuilder) LightParamsTemperature(colorTemp int, onOff int) map[string]any {
	return map[string]any{
		"color_temp": colorTemp,
		"on_off":     onOff,
	}
}

// LightParams creates light parameter map for HSV color
func (cb *CommandBuilder) LightParamsHSV(hue, saturation, brightness, onOff int) map[string]any {
	return map[string]any{
		"hue":        hue,
		"saturation": saturation,
		"brightness": brightness,
		"on_off":     onOff,
	}
}
