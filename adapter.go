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
	pluginCtx runner.PluginContext
	client    kasa.Client
	macMu     sync.RWMutex
	macToIP   map[string]string // normalized MAC → IP
	runCtx    context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewPluginKasaPlugin creates a new PluginKasaPlugin instance
func NewPluginKasaPlugin() *PluginKasaPlugin {
	return &PluginKasaPlugin{}
}

func (p *PluginKasaPlugin) Initialize(ctx runner.PluginContext) (types.Manifest, error) {
	p.pluginCtx = ctx
	p.client = kasa.NewRealClient()
	p.macToIP = make(map[string]string)

	return types.Manifest{ID: "plugin-kasa", Name: "Kasa Plugin", Version: "1.0.0", Schemas: types.CoreDomains()}, nil
}

func (p *PluginKasaPlugin) Start(ctx context.Context) error {
	p.runCtx, p.cancel = context.WithCancel(context.Background())
	p.ensureCoreState()
	p.reconcileRegistry()

	stop, err := p.client.ListenUDP(func(ip string, info kasa.KasaSysInfo) {
		mac := normalizeMac(info.Mac)
		if mac != "" {
			p.setIPForMAC(mac, ip)
		}
	})
	if err != nil {
		// UDP listener failed — discovery relies on static IPs only
		stop = func() {}
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		<-p.runCtx.Done()
		stop()
	}()

	if boolEnv("KASA_DISCOVERY_ACTIVE", true) {
		intervalSec := intEnv("KASA_DISCOVERY_SCAN_INTERVAL_SEC", 180)
		interval := time.Duration(intervalSec) * time.Second
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-p.runCtx.Done():
					return
				case <-t.C:
					p.reconcileRegistry()
				}
			}
		}()
	}

	_ = ctx
	return nil
}

func (p *PluginKasaPlugin) Stop() error {
	if p.cancel != nil {
		p.cancel()
		p.wg.Wait()
	}
	return nil
}

func (p *PluginKasaPlugin) OnReset() error {
	if p.pluginCtx.Registry == nil {
		return nil
	}
	for _, dev := range p.pluginCtx.Registry.LoadDevices() {
		_ = p.pluginCtx.Registry.DeleteDevice(dev.ID)
	}
	return p.pluginCtx.Registry.DeleteState()
}

func (p *PluginKasaPlugin) ensureCoreState() {
	if p.pluginCtx.Registry == nil {
		return
	}
	coreID := types.CoreDeviceID("plugin-kasa")
	_ = p.pluginCtx.Registry.SaveDevice(types.Device{
		ID:         coreID,
		SourceID:   coreID,
		SourceName: "plugin-kasa",
		LocalName:  "plugin-kasa",
	})
	for _, ent := range types.CoreEntities("plugin-kasa") {
		_ = p.pluginCtx.Registry.SaveEntity(ent)
	}
}

func (p *PluginKasaPlugin) reconcileRegistry() {
	if p.pluginCtx.Registry == nil {
		return
	}
	devices, err := p.discoverDevices()
	if err != nil {
		return
	}

	for _, dev := range devices {
		if dev.ID == "" {
			continue
		}
		_ = p.pluginCtx.Registry.SaveDevice(dev)
		if dev.ID == types.CoreDeviceID("plugin-kasa") {
			continue
		}
		entities, err := p.entitiesForDevice(dev.ID)
		if err != nil {
			continue
		}
		for _, ent := range entities {
			_ = p.pluginCtx.Registry.SaveEntity(ent)
		}
	}
}

// discoverDevices returns the current list of devices: the core plugin device
// plus one device per responding Kasa device. It also populates macToIP as a
// side effect so subsequent entitiesForDevice calls can find their IPs.
func (p *PluginKasaPlugin) discoverDevices() ([]types.Device, error) {
	coreID := types.CoreDeviceID("plugin-kasa")
	byID := map[string]types.Device{
		coreID: {ID: coreID, SourceID: coreID, SourceName: "Kasa Plugin"},
	}

	candidates := parseIPs(os.Getenv("KASA_STATIC_IPS"))
	candidates = append(candidates, subnetCandidates()...)

	// Include any IPs already learned via UDP.
	p.macMu.RLock()
	for _, ip := range p.macToIP {
		candidates = append(candidates, ip)
	}
	p.macMu.RUnlock()

	if len(candidates) == 0 {
		out := make([]types.Device, 0, len(byID))
		for _, dev := range byID {
			out = append(out, dev)
		}
		return out, nil
	}

	// Deduplicate
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
			p.setIPForMAC(mac, ip)

			mu.Lock()
			defer mu.Unlock()

			if len(info.Children) > 0 {
				for _, child := range info.Children {
					childID := mac + "-" + child.ID
					byID[childID] = types.Device{
						ID:         childID,
						SourceID:   childID,
						SourceName: "Kasa " + info.Model + " Outlet",
						LocalName:  child.Alias,
					}
				}
			} else {
				byID[mac] = types.Device{
					ID:         mac,
					SourceID:   mac,
					SourceName: "Kasa " + info.Model,
					LocalName:  info.Alias,
				}
			}
		}()
	}
	wg.Wait()

	out := make([]types.Device, 0, len(byID))
	for _, dev := range byID {
		out = append(out, dev)
	}
	return out, nil
}

// entitiesForDevice returns the entities for the given device ID.
func (p *PluginKasaPlugin) entitiesForDevice(deviceID string) ([]types.Entity, error) {
	mac := normalizeMac(strings.Split(deviceID, "-")[0])
	ip := p.getIPForMAC(mac)
	if ip == "" {
		return nil, nil
	}

	info, err := p.client.GetSysInfo(ip)
	if err != nil {
		return nil, nil
	}

	var entities []types.Entity
	if len(info.Children) > 0 {
		parts := strings.Split(deviceID, "-")
		if len(parts) > 1 {
			childID := parts[1]
			for _, child := range info.Children {
				if child.ID == childID {
					entities = append(entities, types.Entity{
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
		entities = append(entities, types.Entity{
			ID:        "light",
			DeviceID:  deviceID,
			Domain:    light.Type,
			LocalName: "Light",
			Actions:   light.SupportedActions(),
		})
	} else {
		entities = append(entities, types.Entity{
			ID:        "power",
			DeviceID:  deviceID,
			Domain:    entityswitch.Type,
			LocalName: "Switch",
			Actions:   entityswitch.SupportedActions(),
		})
	}

	return entities, nil
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
	if err != nil || info == nil {
		return
	}

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
		payload, _ = json.Marshal(lightState)
	} else {
		payload, _ = json.Marshal(entityswitch.State{Power: power})
	}

	if p.pluginCtx.Events == nil {
		return
	}
	_ = p.pluginCtx.Events.PublishEvent(types.InboundEvent{
		DeviceID: deviceID,
		EntityID: entityID,
		Payload:  json.RawMessage(payload),
	})
}

func (p *PluginKasaPlugin) runCommand(req types.Command, entity types.Entity) (types.Entity, error) {
	mac := normalizeMac(strings.Split(entity.DeviceID, "-")[0])
	ip := p.getIPForMAC(mac)

	if ip == "" {
		err := fmt.Errorf("IP not found for device %s", entity.DeviceID)
		p.setEntityError(&entity, err)
		return entity, err
	}

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
		mappedErr := p.mapNetworkError(err)
		p.setEntityError(&entity, mappedErr)
		return entity, mappedErr
	}

	p.setEntitySuccess(&entity)

	go func() {
		time.Sleep(500 * time.Millisecond)
		p.emitDeviceState(entity.DeviceID)
	}()

	return entity, nil
}

func (p *PluginKasaPlugin) OnCommand(req types.Command, entity types.Entity) error {
	_, err := p.runCommand(req, entity)
	return err
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

// setEntityError sets the entity state to failed with error information
func (p *PluginKasaPlugin) setEntityError(entity *types.Entity, err error) {
	entity.Data.SyncStatus = types.SyncStatusFailed
	entity.Data.UpdatedAt = time.Now()
	errorState := map[string]interface{}{"error": err.Error()}
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
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
		return kasa.ErrTimeout
	}
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no route to host") || strings.Contains(errStr, "host is down") {
		return kasa.ErrOffline
	}
	if strings.Contains(errStr, "network") || strings.Contains(errStr, "dial") {
		return kasa.ErrNetwork
	}
	if strings.Contains(errStr, "invalid") || strings.Contains(errStr, "unmarshal") {
		return kasa.ErrInvalidResponse
	}
	return fmt.Errorf("%w: %v", kasa.ErrUnknown, err)
}

// getIPForMAC retrieves the IP address for a given MAC from the in-memory map.
func (p *PluginKasaPlugin) getIPForMAC(mac string) string {
	p.macMu.RLock()
	defer p.macMu.RUnlock()
	return p.macToIP[mac]
}

// setIPForMAC stores a MAC → IP mapping in the in-memory map.
func (p *PluginKasaPlugin) setIPForMAC(mac, ip string) {
	p.macMu.Lock()
	defer p.macMu.Unlock()
	if p.macToIP == nil {
		p.macToIP = make(map[string]string)
	}
	p.macToIP[mac] = ip
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
