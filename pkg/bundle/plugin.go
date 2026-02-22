package bundle

import (
	 "github.com/slidebolt/plugin-kasa/pkg/device"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
	"github.com/slidebolt/plugin-sdk"
	"time"
)

type KasaPlugin struct {
	bundle sdk.Bundle
	client logic.KasaClient
}

func (p *KasaPlugin) Init(b sdk.Bundle) error {
	p.bundle = b
	b.UpdateMetadata("TP-Link Kasa")
	p.client = logic.DefaultConstructor()
	b.Log().Info("Kasa Plugin Initializing...")

	// Start UDP Listener for discovery
	p.client.ListenUDP(func(ip string, info logic.KasaSysInfo) {
		device.Register(p.bundle, p.client, ip, info)
	})

	// Initial Probe
	p.client.SendUDPProbe()

	// Background Discovery Polling
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			p.client.SendUDPProbe()
		}
	}()

	return nil
}

func NewPlugin() sdk.Plugin {
	return &KasaPlugin{}
}
