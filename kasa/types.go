package kasa

// KasaResponse is the top-level JSON envelope returned by the Kasa TCP API.
type KasaResponse struct {
	System struct {
		SysInfo       KasaSysInfo `json:"get_sysinfo"`
		SetRelayState struct {
			ErrCode int `json:"err_code"`
		} `json:"set_relay_state,omitempty"`
	} `json:"system"`
	Bulb struct {
		TransitionLightState struct {
			ErrCode int `json:"err_code"`
		} `json:"transition_light_state,omitempty"`
	} `json:"smartlife.iot.smartbulb.lightingservice,omitempty"`
}

// KasaSysInfo holds the device identity, relay state, and optional light state
// returned by the Kasa get_sysinfo command.
type KasaSysInfo struct {
	Alias      string `json:"alias"`
	Model      string `json:"model"`
	Mac        string `json:"mac"`
	DevType    string `json:"type"`
	MicType    string `json:"mic_type"`
	RelayState int    `json:"relay_state"`
	// Light state (bulbs only)
	LightState *KasaLightState `json:"light_state,omitempty"`
	Children   []struct {
		ID    string `json:"id"`
		Alias string `json:"alias"`
		State int    `json:"state"`
	} `json:"children,omitempty"`
}

// KasaLightState carries the brightness, color temperature, and HSB values
// reported by Kasa smart bulbs inside the get_sysinfo response.
type KasaLightState struct {
	OnOff      int  `json:"on_off"`
	Brightness int  `json:"brightness"`
	ColorTemp  *int `json:"color_temp,omitempty"`
	Hue        *int `json:"hue,omitempty"`
	Saturation *int `json:"saturation,omitempty"`
}

// IsLight returns true if this device is a smart bulb.
func (s *KasaSysInfo) IsLight() bool {
	if s.DevType == "IOT.SMARTBULB" || s.MicType == "IOT.SMARTBULB" {
		return true
	}
	// Some devices might not have DevType or MicType set clearly but have LightState
	if s.LightState != nil {
		return true
	}
	return false
}

func (s *KasaSysInfo) IsColor() bool {
	return s.LightState != nil && (s.LightState.Hue != nil || s.LightState.Saturation != nil)
}

func (s *KasaSysInfo) IsTemp() bool {
	return s.LightState != nil && s.LightState.ColorTemp != nil
}

// ChildPower returns the on/off state for a child outlet by ID.
func (s *KasaSysInfo) ChildPower(childID string) bool {
	for _, child := range s.Children {
		if child.ID == childID {
			return child.State == 1
		}
	}
	return false
}
