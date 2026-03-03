package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/slidebolt/plugin-kasa/kasa"
	"github.com/slidebolt/sdk-entities/light"
	entityswitch "github.com/slidebolt/sdk-entities/switch"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

type PluginKasaPlugin struct {
	sink      runner.EventSink
	client    kasa.Client
	mu        sync.RWMutex
	ipMap     map[string]string       // mac -> ip
	deviceMap map[string]types.Device // id -> device
	failures  map[string]int          // id -> consecutive failures
	ctx       context.Context
	cancel    context.CancelFunc
	rawStore  runner.RawStore
}

func (p *PluginKasaPlugin) OnInitialize(config runner.Config, state types.Storage) (types.Manifest, types.Storage) {
	p.sink = config.EventSink
	p.client = kasa.NewRealClient()
	p.ipMap = make(map[string]string)
	p.deviceMap = make(map[string]types.Device)
	p.failures = make(map[string]int)
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.rawStore = config.RawStore

	if len(state.Data) > 0 {
		_ = json.Unmarshal(state.Data, &p.ipMap)
	}

	return types.Manifest{ID: "plugin-kasa", Name: "Kasa Plugin", Version: "1.0.0"}, state
}

func (p *PluginKasaPlugin) OnReady() {
	go p.discoveryLoop()
	go p.pollingLoop()
}

func (p *PluginKasaPlugin) WaitReady(ctx context.Context) error {
	return nil
}

func (p *PluginKasaPlugin) OnShutdown() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *PluginKasaPlugin) discoveryLoop() {
	// Start UDP listener
	stop, err := p.client.ListenUDP(func(ip string, info kasa.KasaSysInfo) {
		mac := normalizeMac(info.Mac)
		p.mu.Lock()
		p.ipMap[mac] = ip
		p.mu.Unlock()
	})
	if err != nil {
		log.Printf("[ERROR] Kasa failed to start UDP listener: %v", err)
	} else {
		go func() {
			<-p.ctx.Done()
			stop()
		}()
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		if err := p.client.SendUDPProbe(); err != nil {
			log.Printf("[WARN] Kasa failed to send UDP probe: %v", err)
		}
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *PluginKasaPlugin) pollingLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		p.mu.RLock()
		devices := make([]types.Device, 0, len(p.deviceMap))
		for _, dev := range p.deviceMap {
			devices = append(devices, dev)
		}
		p.mu.RUnlock()

		for _, dev := range devices {
			p.pollDevice(dev)
		}

		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (p *PluginKasaPlugin) pollDevice(dev types.Device) {
	mac := normalizeMac(strings.Split(dev.SourceID, "-")[0])
	p.mu.RLock()
	ip, ok := p.ipMap[mac]
	if !ok {
		// Try to get IP from config if not discovered yet
		var cfg struct {
			IP string `json:"ip"`
		}
		if p.rawStore != nil {
			if raw, err := p.rawStore.ReadRawDevice(dev.ID); err == nil {
				json.Unmarshal(raw, &cfg)
			}
		}
		ip = cfg.IP
	}
	failCount := p.failures[dev.ID]
	p.mu.RUnlock()

	if ip == "" {
		return
	}

	// Simple backoff: if we failed recently, skip some polls
	if failCount > 0 {
		backoff := failCount
		if backoff > 10 {
			backoff = 10
		}
		if time.Now().Unix()%int64(backoff) != 0 {
			return
		}
	}

	info, err := p.client.GetSysInfo(ip)
	if err != nil {
		p.mu.Lock()
		p.failures[dev.ID]++
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	p.failures[dev.ID] = 0
	p.ipMap[mac] = ip
	p.mu.Unlock()

	// Handle multi-outlet devices (children)
	if len(info.Children) > 0 {
		// Find this specific device's state if it's a child
		parts := strings.Split(dev.SourceID, "-")
		if len(parts) > 1 {
			childID := parts[1]
			for _, child := range info.Children {
				if child.ID == childID {
					p.emitState(dev.ID, "power", child.State == 1, nil)
					break
				}
			}
		}
	} else if info.IsLight() {
		state := light.State{
			Power: info.RelayState == 1,
		}
		if info.LightState != nil {
			state.Power = info.LightState.OnOff == 1
			state.Brightness = info.LightState.Brightness
			if info.LightState.ColorTemp != nil {
				state.Temperature = *info.LightState.ColorTemp
			}
			if info.LightState.Hue != nil && info.LightState.Saturation != nil {
				r, g, b := hsvToRgb(*info.LightState.Hue, *info.LightState.Saturation, info.LightState.Brightness)
				state.RGB = []int{r, g, b}
			}
		}
		p.emitState(dev.ID, "light", state.Power, &state)
	} else {
		p.emitState(dev.ID, "power", info.RelayState == 1, nil)
	}
}

func (p *PluginKasaPlugin) emitState(deviceID, entityID string, power bool, lightState *light.State) {
	var payload []byte
	if lightState != nil {
		payload, _ = json.Marshal(lightState)
	} else {
		payload, _ = json.Marshal(entityswitch.State{Power: power})
	}

	p.sink.EmitTypedEvent(types.InboundEventTyped[types.GenericPayload]{
		DeviceID: deviceID,
		EntityID: entityID,
		Payload:  rawToGeneric(payload),
	})
}

func rawToGeneric(raw []byte) types.GenericPayload {
	out := types.GenericPayload{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func (p *PluginKasaPlugin) OnHealthCheck() (string, error) { return "perfect", nil }

func (p *PluginKasaPlugin) OnStorageUpdate(current types.Storage) (types.Storage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	b, _ := json.Marshal(p.ipMap)
	current.Data = b
	return current, nil
}

func (p *PluginKasaPlugin) OnDeviceCreate(dev types.Device) (types.Device, error) {
	p.mu.Lock()
	p.deviceMap[dev.ID] = dev
	mac := normalizeMac(strings.Split(dev.SourceID, "-")[0])
	var cfg struct {
		IP string `json:"ip"`
	}
	if p.rawStore != nil {
		if raw, err := p.rawStore.ReadRawDevice(dev.ID); err == nil {
			json.Unmarshal(raw, &cfg)
		}
	}
	if cfg.IP != "" {
		p.ipMap[mac] = cfg.IP
	}
	p.mu.Unlock()
	return dev, nil
}

func (p *PluginKasaPlugin) OnDeviceUpdate(dev types.Device) (types.Device, error) {
	p.mu.Lock()
	p.deviceMap[dev.ID] = dev
	p.mu.Unlock()
	return dev, nil
}

func (p *PluginKasaPlugin) OnDeviceDelete(id string) error {
	p.mu.Lock()
	delete(p.deviceMap, id)
	p.mu.Unlock()
	return nil
}

func (p *PluginKasaPlugin) OnDevicesList(current []types.Device) ([]types.Device, error) {
	p.mu.Lock()
	existing := make(map[string]types.Device)
	for _, dev := range current {
		existing[dev.ID] = dev
		p.deviceMap[dev.ID] = dev
		mac := normalizeMac(strings.Split(dev.SourceID, "-")[0])
		var cfg struct {
			IP string `json:"ip"`
		}
		if p.rawStore != nil {
			if raw, err := p.rawStore.ReadRawDevice(dev.ID); err == nil {
				json.Unmarshal(raw, &cfg)
			}
		}
		if cfg.IP != "" && p.ipMap[mac] == "" {
			p.ipMap[mac] = cfg.IP
		}
	}

	// Snapshot IP map to perform I/O outside the lock
	ips := make(map[string]string)
	for m, ip := range p.ipMap {
		ips[m] = ip
	}
	p.mu.Unlock()

	var newDevices []types.Device
	// Inject discovered devices
	for mac, ip := range ips {
		// Only poll if the base MAC device isn't registered yet
		if _, ok := existing[mac]; !ok {
			info, err := p.client.GetSysInfo(ip)
			if err != nil {
				continue
			}

			cfgData, _ := json.Marshal(map[string]string{"ip": ip})

			// If it has children, register each child as a device
			if len(info.Children) > 0 {
				for _, child := range info.Children {
					childID := mac + "-" + child.ID
					if _, exists := existing[childID]; !exists {
						if p.rawStore != nil {
							_ = p.rawStore.WriteRawDevice(childID, cfgData)
						}
						dev := types.Device{
							ID:         childID,
							SourceID:   childID,
							SourceName: "Kasa " + info.Model + " Outlet",
							LocalName:  child.Alias,
						}
						newDevices = append(newDevices, dev)
					}
				}
			} else {
				if p.rawStore != nil {
					_ = p.rawStore.WriteRawDevice(mac, cfgData)
				}
				dev := runner.ReconcileDevice(types.Device{}, types.Device{
					ID:         mac,
					SourceID:   mac,
					SourceName: "Kasa " + info.Model,
					LocalName:  info.Alias,
				})
				newDevices = append(newDevices, dev)
			}
		}
	}

	if len(newDevices) > 0 {
		p.mu.Lock()
		for _, d := range newDevices {
			p.deviceMap[d.ID] = d
			current = append(current, d)
		}
		p.mu.Unlock()
	}

	return current, nil
}

func (p *PluginKasaPlugin) OnDeviceSearch(q types.SearchQuery, res []types.Device) ([]types.Device, error) {
	return res, nil
}

func (p *PluginKasaPlugin) OnEntityCreate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginKasaPlugin) OnEntityUpdate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginKasaPlugin) OnEntityDelete(d, e string) error                    { return nil }

func (p *PluginKasaPlugin) OnEntitiesList(deviceID string, current []types.Entity) ([]types.Entity, error) {
	p.mu.RLock()
	dev, ok := p.deviceMap[deviceID]
	if !ok {
		p.mu.RUnlock()
		return current, nil
	}
	mac := normalizeMac(strings.Split(dev.SourceID, "-")[0])
	ip := p.ipMap[mac]
	p.mu.RUnlock()

	// Try to poll if we don't have enough info
	if ip == "" {
		return current, nil
	}

	info, err := p.client.GetSysInfo(ip)
	if err != nil {
		return current, nil
	}

	var discovered []types.Entity
	if len(info.Children) > 0 {
		// Identify which child this device represents
		parts := strings.Split(dev.SourceID, "-")
		if len(parts) > 1 {
			childID := parts[1]
			for _, child := range info.Children {
				if child.ID == childID {
					discovered = append(discovered, types.Entity{
						ID:        "power",
						DeviceID:  deviceID,
						Domain:    entityswitch.Type,
						LocalName: "Outlet",
						Actions:   entityswitch.SupportedActions(),
					})
					break
				}
			}
		}
	} else if info.IsLight() {
		discovered = append(discovered, types.Entity{
			ID:        "light",
			DeviceID:  deviceID,
			Domain:    light.Type,
			LocalName: "Light",
			Actions:   light.SupportedActions(),
		})
	} else {
		discovered = append(discovered, types.Entity{
			ID:        "power",
			DeviceID:  deviceID,
			Domain:    entityswitch.Type,
			LocalName: "Switch",
			Actions:   entityswitch.SupportedActions(),
		})
	}

	for _, d := range discovered {
		if !entityExists(current, d.ID) {
			current = append(current, d)
		}
	}

	return current, nil
}

func entityExists(current []types.Entity, id string) bool {
	for _, e := range current {
		if e.ID == id {
			return true
		}
	}
	return false
}

func (p *PluginKasaPlugin) OnCommandTyped(req types.CommandRequest[types.GenericPayload], entity types.Entity) (types.Entity, error) {
	p.mu.RLock()
	dev, ok := p.deviceMap[entity.DeviceID]
	if !ok {
		p.mu.RUnlock()
		return entity, fmt.Errorf("device not found")
	}
	mac := normalizeMac(strings.Split(dev.SourceID, "-")[0])
	ip := p.ipMap[mac]
	p.mu.RUnlock()

	if ip == "" {
		return entity, fmt.Errorf("IP not found for device %s", entity.DeviceID)
	}

	// Extract child ID if any from SourceID
	childID := ""
	parts := strings.Split(dev.SourceID, "-")
	if len(parts) > 1 {
		childID = parts[1]
	}

	var err error
	switch entity.Domain {
	case light.Type:
		var lcmd light.Command
		if err := decodeGenericPayload(req.Payload, &lcmd); err != nil {
			return entity, err
		}
		if err := light.ValidateCommand(lcmd); err != nil {
			return entity, err
		}
		err = p.handleLightCommand(ip, childID, lcmd)
	case entityswitch.Type:
		var scmd entityswitch.Command
		if err := decodeGenericPayload(req.Payload, &scmd); err != nil {
			return entity, err
		}
		if err := entityswitch.ValidateCommand(scmd); err != nil {
			return entity, err
		}
		err = p.handleSwitchCommand(ip, childID, scmd)
	default:
		return entity, fmt.Errorf("unsupported entity domain: %s", entity.Domain)
	}

	if err != nil {
		return entity, err
	}

	// Optimistically update entity data
	go func() {
		time.Sleep(500 * time.Millisecond) // Give it a moment to apply
		p.pollDevice(dev)
	}()

	return entity, nil
}

func (p *PluginKasaPlugin) handleLightCommand(ip, childID string, cmd light.Command) error {
	switch cmd.Type {
	case light.ActionTurnOn:
		return p.client.SetPower(ip, childID, 1)
	case light.ActionTurnOff:
		return p.client.SetPower(ip, childID, 0)
	case light.ActionSetBrightness:
		return p.client.SetLightState(ip, map[string]any{"brightness": *cmd.Brightness, "on_off": 1})
	case light.ActionSetTemperature:
		return p.client.SetLightState(ip, map[string]any{"color_temp": *cmd.Temperature, "on_off": 1})
	case light.ActionSetRGB:
		// Convert RGB to HSV for Kasa
		h, s, v := rgbToHsv((*cmd.RGB)[0], (*cmd.RGB)[1], (*cmd.RGB)[2])
		return p.client.SetLightState(ip, map[string]any{"hue": h, "saturation": s, "brightness": v, "on_off": 1})
	}
	return nil
}

func (p *PluginKasaPlugin) handleSwitchCommand(ip, childID string, cmd entityswitch.Command) error {
	switch cmd.Type {
	case entityswitch.ActionTurnOn:
		return p.client.SetPower(ip, childID, 1)
	case entityswitch.ActionTurnOff:
		return p.client.SetPower(ip, childID, 0)
	}
	return nil
}

func (p *PluginKasaPlugin) OnEventTyped(evt types.EventTyped[types.GenericPayload], entity types.Entity) (types.Entity, error) {
	// Sync entity state from event
	if entity.Domain == light.Type {
		store := light.Bind(&entity)
		var levt light.Event
		if err := decodeGenericPayload(evt.Payload, &levt); err != nil {
			return entity, err
		}
		if err := light.ValidateEvent(levt); err != nil {
			return entity, err
		}
		if err := store.SetReportedFromEvent(levt); err != nil {
			return entity, err
		}
	} else if entity.Domain == entityswitch.Type {
		store := entityswitch.Bind(&entity)
		var sevt entityswitch.Event
		if err := decodeGenericPayload(evt.Payload, &sevt); err != nil {
			return entity, err
		}
		if err := entityswitch.ValidateEvent(sevt); err != nil {
			return entity, err
		}
		if err := store.SetReportedFromEvent(sevt); err != nil {
			return entity, err
		}
	}
	return entity, nil
}

func decodeGenericPayload(payload types.GenericPayload, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func normalizeMac(mac string) string {
	m := strings.ToLower(mac)
	m = strings.ReplaceAll(m, ":", "")
	m = strings.ReplaceAll(m, "-", "")
	return m
}

func rgbToHsv(r, g, b int) (h, s, v int) {
	rf := float64(r) / 255.0
	gf := float64(g) / 255.0
	bf := float64(b) / 255.0

	max := rf
	if gf > max {
		max = gf
	}
	if bf > max {
		max = bf
	}

	min := rf
	if gf < min {
		min = gf
	}
	if bf < min {
		min = bf
	}

	v = int(max * 100)
	delta := max - min

	if max != 0 {
		s = int((delta / max) * 100)
	} else {
		return 0, 0, 0
	}

	if delta == 0 {
		return 0, 0, v
	}

	if rf == max {
		h = int(60 * (gf - bf) / delta)
	} else if gf == max {
		h = int(120 + 60*(bf-rf)/delta)
	} else {
		h = int(240 + 60*(rf-gf)/delta)
	}

	if h < 0 {
		h += 360
	}
	return h, s, v
}

func hsvToRgb(h, s, v int) (r, g, b int) {
	hf := float64(h)
	sf := float64(s) / 100.0
	vf := float64(v) / 100.0

	if s == 0 {
		iv := int(vf * 255)
		return iv, iv, iv
	}

	hi := int(hf/60.0) % 6
	f := hf/60.0 - float64(hi)
	p := vf * (1.0 - sf)
	q := vf * (1.0 - f*sf)
	t := vf * (1.0 - (1.0-f)*sf)

	var rf, gf, bf float64
	switch hi {
	case 0:
		rf, gf, bf = vf, t, p
	case 1:
		rf, gf, bf = q, vf, p
	case 2:
		rf, gf, bf = p, vf, t
	case 3:
		rf, gf, bf = p, q, vf
	case 4:
		rf, gf, bf = t, p, vf
	case 5:
		rf, gf, bf = vf, p, q
	}

	return int(rf * 255), int(gf * 255), int(bf * 255)
}

func main() {
	r, err := runner.NewRunner(&PluginKasaPlugin{})
	if err != nil {
		log.Fatal(err)
	}
	if err := r.Run(); err != nil {
		log.Fatal(err)
	}
}
