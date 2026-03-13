package main

import (
	"testing"

	"github.com/slidebolt/sdk-types"
)

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
