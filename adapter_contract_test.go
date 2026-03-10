package main

import (
	"testing"

	"github.com/slidebolt/sdk-types"
)

func TestOnDeviceDiscover_RetainsManuallyCreatedCurrentDevice(t *testing.T) {
	p := &PluginKasaPlugin{}

	current := []types.Device{
		{
			ID:         "manual-device-1",
			SourceID:   "manual-device-1",
			SourceName: "Manual Device",
			LocalName:  "Manual Device",
		},
	}

	devices, err := p.OnDeviceDiscover(current)
	if err != nil {
		t.Fatalf("OnDeviceDiscover failed: %v", err)
	}

	found := false
	for _, d := range devices {
		if d.ID == "manual-device-1" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected manually created device to remain in current slice result")
	}
}
