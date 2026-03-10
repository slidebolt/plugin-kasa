package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slidebolt/plugin-kasa/kasa"
	"github.com/slidebolt/sdk-entities/light"
	entityswitch "github.com/slidebolt/sdk-entities/switch"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

// PluginKasaPlugin implements the Slidebolt SDK Plugin interface for Kasa devices
type PluginKasaPlugin struct {
	sink     runner.EventSink
	client   kasa.Client
	mu       sync.RWMutex
	failures map[string]int // id -> consecutive failures
	ctx      context.Context
	cancel   context.CancelFunc
	rawStore runner.RawStore
	lastScan time.Time
	scanMu   sync.Mutex
}

// NewPluginKasaPlugin creates a new PluginKasaPlugin instance
func NewPluginKasaPlugin() *PluginKasaPlugin {
	return &PluginKasaPlugin{}
}

func (p *PluginKasaPlugin) OnInitialize(config runner.Config, state types.Storage) (types.Manifest, types.Storage) {
	p.sink = config.EventSink
	p.client = kasa.NewRealClient()
	p.failures = make(map[string]int)
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.rawStore = config.RawStore

	return types.Manifest{ID: "plugin-kasa", Name: "Kasa Plugin", Version: "1.0.0", Schemas: types.CoreDomains()}, state
}

func (p *PluginKasaPlugin) OnReady() {
	// Background discovery is disabled per SDK principles
	// Discovery happens lazily in OnDeviceDiscover
	// We only set up the UDP listener to receive broadcast responses
	stop, err := p.client.ListenUDP(func(ip string, info kasa.KasaSysInfo) {
		mac := normalizeMac(info.Mac)
		// Store MAC-to-IP mapping in RawStore
		if p.rawStore != nil {
			cfgData, _ := json.Marshal(map[string]string{"ip": ip})
			_ = p.rawStore.WriteRawDevice(mac, cfgData)
		}
	})
	if err != nil {
		// UDP listener failed - discovery will rely on static IPs only
		return
	}
	go func() {
		<-p.ctx.Done()
		stop()
	}()
}

func (p *PluginKasaPlugin) WaitReady(ctx context.Context) error {
	return nil
}

func (p *PluginKasaPlugin) OnShutdown() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *PluginKasaPlugin) OnHealthCheck() (string, error) { return "perfect", nil }

func (p *PluginKasaPlugin) OnConfigUpdate(current types.Storage) (types.Storage, error) {
	// IP mappings are now stored in RawStore, not in Storage
	return current, nil
}

func (p *PluginKasaPlugin) OnDeviceCreate(dev types.Device) (types.Device, error) {
	// Store device-specific config in RawStore if IP is provided
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
		// Store MAC-to-IP mapping in RawStore
		if p.rawStore != nil {
			macData, _ := json.Marshal(map[string]string{"ip": cfg.IP})
			_ = p.rawStore.WriteRawDevice(mac, macData)
		}
	}
	return dev, nil
}

func (p *PluginKasaPlugin) OnDeviceUpdate(dev types.Device) (types.Device, error) {
	// No longer maintaining deviceMap - SDK manages device state
	return dev, nil
}

func (p *PluginKasaPlugin) OnDeviceDelete(id string) error {
	// No longer maintaining deviceMap - SDK manages device state
	return nil
}

func (p *PluginKasaPlugin) OnDeviceDiscover(current []types.Device) ([]types.Device, error) {
	// Build existing device lookup map from current slice
	existing := make(map[string]types.Device)
	for _, dev := range current {
		existing[dev.ID] = dev

		// Load device-specific IP config and store in RawStore as MAC-to-IP mapping
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
			// Store/update MAC-to-IP mapping in RawStore
			if p.rawStore != nil {
				// Check if we already have this mapping
				if existingRaw, err := p.rawStore.ReadRawDevice(mac); err != nil || len(existingRaw) == 0 {
					macData, _ := json.Marshal(map[string]string{"ip": cfg.IP})
					_ = p.rawStore.WriteRawDevice(mac, macData)
				}
			}
		}
	}

	// Get candidate IPs for discovery from environment
	candidates := parseIPs(os.Getenv("KASA_STATIC_IPS"))
	candidates = append(candidates, subnetCandidates()...)

	// Also query RawStore for any known MAC-to-IP mappings we've discovered previously
	// Note: We can't easily enumerate all keys in RawStore, so we rely on:
	// 1. UDP discovery callback storing mappings
	// 2. Static IPs from environment
	// 3. Device config lookups during command handling

	var newDevices []types.Device

	// Perform active discovery on candidate IPs
	if len(candidates) > 0 {
		seen := map[string]struct{}{}
		uniq := make([]string, 0, len(candidates))
		for _, ip := range candidates {
			if _, ok := seen[ip]; ok {
				continue
			}
			seen[ip] = struct{}{}
			uniq = append(uniq, ip)
		}

		timeoutMs := intEnv("KASA_DISCOVERY_TIMEOUT_MS", 400)
		concurrency := intEnv("KASA_DISCOVERY_CONCURRENCY", 64)
		timeout := time.Duration(timeoutMs) * time.Millisecond
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, ip := range uniq {
			ip := ip
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				info, err := quickGetSysInfo(ip, timeout)
				if err != nil {
					return
				}
				mac := normalizeMac(info.Mac)
				if mac == "" {
					return
				}

				// Store MAC-to-IP mapping in RawStore
				if p.rawStore != nil {
					cfgData, _ := json.Marshal(map[string]string{"ip": ip})
					_ = p.rawStore.WriteRawDevice(mac, cfgData)
				}

				mu.Lock()
				defer mu.Unlock()

				// Only register if the base MAC device isn't already registered
				if _, ok := existing[mac]; !ok {
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
			}()
		}
		wg.Wait()
	}

	if len(newDevices) > 0 {
		current = append(current, newDevices...)
	}

	return runner.EnsureCoreDevice("plugin-kasa", current), nil
}

func (p *PluginKasaPlugin) OnDeviceSearch(q types.SearchQuery, res []types.Device) ([]types.Device, error) {
	return res, nil
}

func (p *PluginKasaPlugin) OnEntityCreate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginKasaPlugin) OnEntityUpdate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginKasaPlugin) OnEntityDelete(d, e string) error                    { return nil }

func (p *PluginKasaPlugin) OnEntityDiscover(deviceID string, current []types.Entity) ([]types.Entity, error) {
	current = runner.EnsureCoreEntities("plugin-kasa", deviceID, current)

	// Get MAC from deviceID (they're the same for Kasa devices)
	mac := normalizeMac(deviceID)

	// Get IP from RawStore
	ip := p.getIPForMAC(mac)
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
		parts := strings.Split(deviceID, "-")
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

// getIPForMAC retrieves the IP address for a given MAC from RawStore
func (p *PluginKasaPlugin) getIPForMAC(mac string) string {
	if p.rawStore == nil {
		return ""
	}
	raw, err := p.rawStore.ReadRawDevice(mac)
	if err != nil {
		return ""
	}
	var cfg struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	return cfg.IP
}

// getDeviceState queries the device and returns the current state
func (p *PluginKasaPlugin) getDeviceState(deviceID string) (*kasa.KasaSysInfo, error) {
	mac := normalizeMac(strings.Split(deviceID, "-")[0])
	ip := p.getIPForMAC(mac)
	if ip == "" {
		return nil, fmt.Errorf("IP not found for device %s", deviceID)
	}
	return p.client.GetSysInfo(ip)
}

// emitDeviceState polls a device and emits its current state as events
func (p *PluginKasaPlugin) emitDeviceState(deviceID string) {
	info, err := p.getDeviceState(deviceID)
	if err != nil {
		return
	}

	// Handle multi-outlet devices (children)
	if len(info.Children) > 0 {
		parts := strings.Split(deviceID, "-")
		if len(parts) > 1 {
			childID := parts[1]
			for _, child := range info.Children {
				if child.ID == childID {
					p.emitState(deviceID, "power", child.State == 1, nil)
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
		p.emitState(deviceID, "light", state.Power, &state)
	} else {
		p.emitState(deviceID, "power", info.RelayState == 1, nil)
	}
}

func (p *PluginKasaPlugin) emitState(deviceID, entityID string, power bool, lightState *light.State) {
	var payload []byte
	if lightState != nil {
		eventType := light.ActionTurnOff
		if lightState.Power {
			eventType = light.ActionTurnOn
		}
		payload, _ = json.Marshal(light.Event{Type: eventType})
	} else {
		eventType := entityswitch.ActionTurnOff
		if power {
			eventType = entityswitch.ActionTurnOn
		}
		payload, _ = json.Marshal(entityswitch.Event{Type: eventType})
	}

	p.sink.EmitEvent(types.InboundEvent{
		DeviceID: deviceID,
		EntityID: entityID,
		Payload:  json.RawMessage(payload),
	})
}

func (p *PluginKasaPlugin) OnCommand(req types.Command, entity types.Entity) (types.Entity, error) {
	// Extract MAC from deviceID (SourceID is typically MAC or MAC-childID)
	mac := normalizeMac(strings.Split(entity.DeviceID, "-")[0])
	ip := p.getIPForMAC(mac)

	if ip == "" {
		err := fmt.Errorf("IP not found for device %s", entity.DeviceID)
		p.setEntityError(&entity, err)
		return entity, err
	}

	// Extract child ID if any from deviceID
	childID := ""
	parts := strings.Split(entity.DeviceID, "-")
	if len(parts) > 1 {
		childID = parts[1]
	}

	var err error
	switch entity.Domain {
	case light.Type:
		var lcmd light.Command
		if err := json.Unmarshal(req.Payload, &lcmd); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := light.ValidateCommand(lcmd); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		err = p.handleLightCommand(ip, childID, lcmd)
	case entityswitch.Type:
		var scmd entityswitch.Command
		if err := json.Unmarshal(req.Payload, &scmd); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := entityswitch.ValidateCommand(scmd); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		err = p.handleSwitchCommand(ip, childID, scmd)
	default:
		err := fmt.Errorf("unsupported entity domain: %s", entity.Domain)
		p.setEntityError(&entity, err)
		return entity, err
	}

	if err != nil {
		// Map network errors to standardized error types
		mappedErr := p.mapNetworkError(err)
		p.setEntityError(&entity, mappedErr)
		return entity, mappedErr
	}

	// Success - set sync status to synced
	p.setEntitySuccess(&entity)

	// Optimistically update entity data
	go func() {
		time.Sleep(500 * time.Millisecond) // Give it a moment to apply
		p.emitDeviceState(entity.DeviceID)
	}()

	return entity, nil
}

func (p *PluginKasaPlugin) handleLightCommand(ip, childID string, cmd light.Command) error {
	builder := kasa.NewCommandBuilder()
	switch cmd.Type {
	case light.ActionTurnOn:
		return p.client.SetPower(ip, childID, 1)
	case light.ActionTurnOff:
		return p.client.SetPower(ip, childID, 0)
	case light.ActionSetBrightness:
		params := builder.LightParamsBrightness(*cmd.Brightness, 1)
		return p.client.SetLightState(ip, params)
	case light.ActionSetTemperature:
		params := builder.LightParamsTemperature(*cmd.Temperature, 1)
		return p.client.SetLightState(ip, params)
	case light.ActionSetRGB:
		// Convert RGB to HSV for Kasa
		h, s, v := rgbToHsv((*cmd.RGB)[0], (*cmd.RGB)[1], (*cmd.RGB)[2])
		params := builder.LightParamsHSV(h, s, v, 1)
		return p.client.SetLightState(ip, params)
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

func (p *PluginKasaPlugin) OnEvent(evt types.Event, entity types.Entity) (types.Entity, error) {
	// Sync entity state from event
	if entity.Domain == light.Type {
		store := light.Bind(&entity)
		var levt light.Event
		if err := json.Unmarshal(evt.Payload, &levt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := light.ValidateEvent(levt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := store.SetReportedFromEvent(levt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		p.setEntitySuccess(&entity)
	} else if entity.Domain == entityswitch.Type {
		store := entityswitch.Bind(&entity)
		var sevt entityswitch.Event
		if err := json.Unmarshal(evt.Payload, &sevt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := entityswitch.ValidateEvent(sevt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		if err := store.SetReportedFromEvent(sevt); err != nil {
			p.setEntityError(&entity, err)
			return entity, err
		}
		p.setEntitySuccess(&entity)
	}
	return entity, nil
}

// setEntityError sets the entity state to failed with error information
func (p *PluginKasaPlugin) setEntityError(entity *types.Entity, err error) {
	// Set sync status to failed
	entity.Data.SyncStatus = types.SyncStatusFailed
	entity.Data.UpdatedAt = time.Now()

	// Create error state with error information
	errorState := map[string]interface{}{
		"error": err.Error(),
	}

	// Marshal error state to reported
	if reportedBytes, jsonErr := json.Marshal(errorState); jsonErr == nil {
		entity.Data.Reported = reportedBytes
	}
}

// setEntitySuccess sets the entity state to synced successfully
func (p *PluginKasaPlugin) setEntitySuccess(entity *types.Entity) {
	entity.Data.SyncStatus = types.SyncStatusSynced
	entity.Data.UpdatedAt = time.Now()
}

// mapNetworkError maps network errors to standardized kasa error types
func (p *PluginKasaPlugin) mapNetworkError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check for timeout-related errors
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
		return kasa.ErrTimeout
	}

	// Check for connection refused or unreachable
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no route to host") || strings.Contains(errStr, "host is down") {
		return kasa.ErrOffline
	}

	// Check for network errors
	if strings.Contains(errStr, "network") || strings.Contains(errStr, "dial") {
		return kasa.ErrNetwork
	}

	// Check for invalid response
	if strings.Contains(errStr, "invalid") || strings.Contains(errStr, "unmarshal") {
		return kasa.ErrInvalidResponse
	}

	// Default to unknown error
	return fmt.Errorf("%w: %v", kasa.ErrUnknown, err)
}

// Helper functions

func boolEnv(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func intEnv(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func parseIPs(list string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, part := range strings.Split(list, ",") {
		ip := strings.TrimSpace(part)
		if ip == "" {
			continue
		}
		if net.ParseIP(ip) == nil {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	return out
}

func subnetCandidates() []string {
	raw := strings.TrimSpace(os.Getenv("KASA_DISCOVERY_SUBNETS"))
	if raw == "" {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	addIP := func(ip string) {
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if strings.Count(token, ".") == 3 && !strings.Contains(token, "/") {
			if net.ParseIP(token) != nil {
				addIP(token)
			}
			continue
		}
		if strings.Count(token, ".") == 2 && !strings.Contains(token, "/") {
			for i := 1; i <= 254; i++ {
				addIP(fmt.Sprintf("%s.%d", token, i))
			}
			continue
		}
		_, cidr, err := net.ParseCIDR(token)
		if err != nil {
			continue
		}
		ip := cidr.IP.To4()
		if ip == nil {
			continue
		}
		maskOnes, bits := cidr.Mask.Size()
		if bits != 32 || maskOnes < 16 || maskOnes > 30 {
			continue
		}
		start := binary.BigEndian.Uint32(ip)
		hostBits := uint32(1) << uint32(32-maskOnes)
		for i := uint32(1); i+1 < hostBits; i++ {
			v := start + i
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, v)
			addIP(net.IP(b).String())
		}
	}
	return out
}

func quickGetSysInfo(ip string, timeout time.Duration) (*kasa.KasaSysInfo, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:9999", ip), timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	builder := kasa.NewCommandBuilder()
	payload := kasa.EncryptWithHeader(builder.GetSysInfo())
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 || length > 16384 {
		return nil, fmt.Errorf("invalid response length %d", length)
	}
	dataBuf := make([]byte, length)
	if _, err := io.ReadFull(conn, dataBuf); err != nil {
		return nil, err
	}
	data := kasa.Decrypt(dataBuf)
	var resp kasa.KasaResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, err
	}
	if resp.System.SysInfo.Mac == "" {
		return nil, fmt.Errorf("empty mac")
	}
	return &resp.System.SysInfo, nil
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
