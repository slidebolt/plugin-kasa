package bundle

import (
	"context"
	"sync"
	"time"

	"github.com/slidebolt/plugin-kasa/pkg/device"
	"github.com/slidebolt/plugin-kasa/pkg/logic"
	"github.com/slidebolt/plugin-sdk"
)

type KasaPlugin struct {
	bundle sdk.Bundle
	client logic.KasaClient
	cancel context.CancelFunc
	wait   func()
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
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	var wg sync.WaitGroup
	p.wait = wg.Wait

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.client.SendUDPProbe()
			}
		}
	}()

	return nil
}

func (p *KasaPlugin) Shutdown() {
	if p.cancel != nil {
		p.cancel()
		p.wait()
	}
}

func NewPlugin() *KasaPlugin { return &KasaPlugin{} }
