// plugin-kasa integrates TP-Link Kasa smart devices with SlideBolt.
//
// Features:
//   - UDP discovery on port 9999 with XOR encryption
//   - TCP control with 4-byte length prefix protocol
//   - Device registration and state management
//   - Switch control commands (on/off)
//   - Connection to messenger and storage SDKs
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	translate "github.com/slidebolt/plugin-kasa/internal/translate"
	contract "github.com/slidebolt/sb-contract"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const PluginID = "plugin-kasa"

func init() {
	domain.Register("kasa_switch", domain.Switch{})
}

var (
	discoverDevices = translate.DiscoverDevices
	getDeviceInfo   = translate.GetDeviceInfo
	setPower        = translate.SetPower
	setChildPower   = translate.SetChildPower
)

type App struct {
	msg    messenger.Messenger
	store  storage.Storage
	cmds   *messenger.Commands
	subs   []messenger.Subscription
	ctx    context.Context
	cancel context.CancelFunc
	ticker *time.Ticker

	mu             sync.RWMutex
	devices        []translate.SysInfo
	ipMap          map[string]string
	seen           map[string]struct{}
	entityChildIDs map[string]string
}

func New() *App { return &App{} }

func (a *App) Hello() contract.HelloResponse {
	return contract.HelloResponse{
		ID:              PluginID,
		Kind:            contract.KindPlugin,
		ContractVersion: contract.ContractVersion,
		DependsOn:       []string{"messenger", "storage"},
	}
}

func (a *App) OnStart(deps map[string]json.RawMessage) (json.RawMessage, error) {
	msg, err := messenger.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect messenger: %w", err)
	}
	a.msg = msg

	storeClient, err := storage.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect storage: %w", err)
	}
	a.store = storeClient

	a.ipMap = make(map[string]string)
	a.seen = make(map[string]struct{})
	a.entityChildIDs = make(map[string]string)

	a.cmds = messenger.NewCommands(msg, domain.LookupCommand)
	sub, err := a.cmds.Receive(PluginID+".>", a.handleCommand)
	if err != nil {
		return nil, fmt.Errorf("subscribe commands: %w", err)
	}
	a.subs = append(a.subs, sub)

	if err := a.discoverAndRegister(); err != nil {
		log.Printf("plugin-kasa: discovery error: %v", err)
	}

	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.ticker = time.NewTicker(30 * time.Second)
	go a.discoveryLoop()

	log.Println("plugin-kasa: started")
	return nil, nil
}

func (a *App) OnShutdown() error {
	if a.cancel != nil {
		a.cancel()
	}
	if a.ticker != nil {
		a.ticker.Stop()
	}
	for _, sub := range a.subs {
		sub.Unsubscribe()
	}
	if a.store != nil {
		a.store.Close()
	}
	if a.msg != nil {
		a.msg.Close()
	}
	return nil
}

func (a *App) discoveryLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.ticker.C:
			if err := a.discoverAndRegister(); err != nil {
				log.Printf("plugin-kasa: discovery refresh error: %v", err)
			}
		}
	}
}

func (a *App) discoverAndRegister() error {
	subnet := os.Getenv("KASA_SUBNET")
	if subnet == "" {
		log.Println("plugin-kasa: KASA_SUBNET not set; skipping discovery")
		return nil
	}

	timeout := 400 * time.Millisecond
	if t := os.Getenv("KASA_TIMEOUT_MS"); t != "" {
		if ms, err := strconv.Atoi(t); err == nil {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	devices, err := discoverDevices(subnet, timeout)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.devices = devices
	a.mu.Unlock()
	for _, dev := range devices {
		a.registerDiscoveredDevice(dev)
	}

	a.refreshIPMappings(subnet)
	return nil
}

func (a *App) refreshIPMappings(subnet string) {
	base := subnet + "."
	for i := 1; i <= 254; i++ {
		ip := base + strconv.Itoa(i)
		go func(targetIP string) {
			if info, err := getDeviceInfo(targetIP); err == nil {
				a.recordIPMappings(*info, targetIP)
			}
		}(ip)
	}
	time.Sleep(2 * time.Second)
}

func (a *App) getDeviceIP(deviceID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if ip, ok := a.ipMap[deviceID]; ok {
		return ip
	}
	return ""
}

func (a *App) handleCommand(addr messenger.Address, cmd any) {
	ip := a.getDeviceIP(addr.DeviceID)
	if ip == "" {
		log.Printf("plugin-kasa: no IP for device %s, attempting refresh", addr.DeviceID)
		ip = a.lookupIPFromStorage(addr.DeviceID)
		if ip == "" {
			log.Printf("plugin-kasa: still no IP for device %s", addr.DeviceID)
			return
		}
	}

	entityKey := domain.EntityKey{
		Plugin:   addr.Plugin,
		DeviceID: addr.DeviceID,
		ID:       addr.EntityID,
	}

	raw, err := a.store.Get(entityKey)
	if err != nil {
		log.Printf("plugin-kasa: command for unknown entity %s: %v", addr.Key(), err)
		return
	}

	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		log.Printf("plugin-kasa: failed to parse entity %s: %v", addr.Key(), err)
		return
	}

	childID := a.getEntityChildID(addr.DeviceID, addr.EntityID)

	switch cmd.(type) {
	case domain.SwitchTurnOn:
		log.Printf("plugin-kasa: switch %s turn_on", addr.Key())
		if err := a.setRelayPower(ip, childID, 1); err != nil {
			log.Printf("plugin-kasa: failed to turn on %s: %v", addr.Key(), err)
		} else {
			a.updateSwitchState(entity, func(s *domain.Switch) { s.Power = true })
		}
	case domain.SwitchTurnOff:
		log.Printf("plugin-kasa: switch %s turn_off", addr.Key())
		if err := a.setRelayPower(ip, childID, 0); err != nil {
			log.Printf("plugin-kasa: failed to turn off %s: %v", addr.Key(), err)
		} else {
			a.updateSwitchState(entity, func(s *domain.Switch) { s.Power = false })
		}
	case domain.SwitchToggle:
		log.Printf("plugin-kasa: switch %s toggle", addr.Key())
		if sw, ok := entity.State.(domain.Switch); ok {
			newState := 0
			if !sw.Power {
				newState = 1
			}
			if err := a.setRelayPower(ip, childID, newState); err != nil {
				log.Printf("plugin-kasa: failed to toggle %s: %v", addr.Key(), err)
			} else {
				a.updateSwitchState(entity, func(s *domain.Switch) { s.Power = !sw.Power })
			}
		}
	default:
		log.Printf("plugin-kasa: unknown command %T for %s", cmd, addr.Key())
	}
}

func (a *App) lookupIPFromStorage(deviceID string) string {
	return ""
}

func (a *App) registerDiscoveredDevice(dev translate.SysInfo) {
	parentDeviceID := translate.MakeDeviceID(dev.Mac)
	currentEntityIDs := make(map[string]struct{})
	deviceType := "kasa_switch"
	if strings.Contains(strings.ToLower(dev.Model), "bulb") ||
		strings.Contains(strings.ToLower(dev.Model), "kl") {
		deviceType = "light"
	}

	if len(dev.Children) == 0 {
		currentEntityIDs[parentDeviceID] = struct{}{}
		a.clearEntityChildID(parentDeviceID, parentDeviceID)
		a.saveDiscoveredEntity(domain.Entity{
			ID:       parentDeviceID,
			Plugin:   PluginID,
			DeviceID: parentDeviceID,
			Type:     deviceType,
			Name:     strings.TrimSpace(dev.Alias),
			Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
			State: domain.Switch{
				Power: relayStateFromSysInfo(dev),
			},
			Meta: map[string]json.RawMessage{
				"model": json.RawMessage(fmt.Sprintf(`"%s"`, dev.Model)),
				"mac":   json.RawMessage(fmt.Sprintf(`"%s"`, dev.Mac)),
			},
		}, strings.TrimSpace(dev.Alias))
	} else {
		for _, child := range dev.Children {
			entityID := translate.MakeChildEntityID(dev.DeviceID, child.ID)
			currentEntityIDs[entityID] = struct{}{}
			a.setEntityChildID(parentDeviceID, entityID, child.ID)
			a.saveDiscoveredEntity(domain.Entity{
				ID:       entityID,
				Plugin:   PluginID,
				DeviceID: parentDeviceID,
				Type:     "kasa_switch",
				Name:     strings.TrimSpace(child.Alias),
				Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
				State: domain.Switch{
					Power: child.RelayState == 1,
				},
				Meta: map[string]json.RawMessage{
					"model": json.RawMessage(fmt.Sprintf(`"%s"`, dev.Model)),
					"mac":   json.RawMessage(fmt.Sprintf(`"%s"`, dev.Mac)),
				},
			}, strings.TrimSpace(child.Alias))
		}
	}
	a.cleanupStaleEntities(parentDeviceID, currentEntityIDs)
}

func (a *App) saveDiscoveredEntity(entity domain.Entity, name string) {
	a.mu.Lock()
	key := entity.Key()
	_, alreadySeen := a.seen[key]
	a.seen[key] = struct{}{}
	a.mu.Unlock()

	if err := a.store.Save(entity); err != nil {
		log.Printf("plugin-kasa: failed to save entity %s: %v", key, err)
	} else if !alreadySeen {
		log.Printf("plugin-kasa: registered %s (%s)", name, key)
	}
}

func relayStateFromSysInfo(dev translate.SysInfo) bool {
	if dev.RelayState != nil {
		return *dev.RelayState == 1
	}
	for _, child := range dev.Children {
		if child.RelayState == 1 {
			return true
		}
	}
	return false
}

func (a *App) recordIPMappings(dev translate.SysInfo, ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	parentDeviceID := translate.MakeDeviceID(dev.Mac)
	a.ipMap[parentDeviceID] = ip
}

func (a *App) setRelayPower(ip, childID string, state int) error {
	if childID != "" {
		return setChildPower(ip, childID, state)
	}
	return setPower(ip, state)
}

func (a *App) setEntityChildID(deviceID, entityID, childID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entityChildIDs[deviceID+"."+entityID] = childID
}

func (a *App) clearEntityChildID(deviceID, entityID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.entityChildIDs, deviceID+"."+entityID)
}

func (a *App) getEntityChildID(deviceID, entityID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.entityChildIDs[deviceID+"."+entityID]
}

func (a *App) cleanupStaleEntities(deviceID string, currentEntityIDs map[string]struct{}) {
	entries, err := a.store.Search(PluginID + "." + deviceID + ".*")
	if err != nil {
		log.Printf("plugin-kasa: search stale entities for %s: %v", deviceID, err)
		return
	}
	for _, entry := range entries {
		parts := strings.Split(entry.Key, ".")
		if len(parts) != 3 {
			continue
		}
		entityID := parts[2]
		if _, ok := currentEntityIDs[entityID]; ok {
			continue
		}
		deleteKey := domain.EntityKey{Plugin: PluginID, DeviceID: deviceID, ID: entityID}
		if err := a.store.Delete(deleteKey); err != nil {
			log.Printf("plugin-kasa: delete stale entity %s: %v", entry.Key, err)
			continue
		}
		a.clearEntityChildID(deviceID, entityID)
	}
}

func (a *App) updateSwitchState(entity domain.Entity, mutate func(*domain.Switch)) {
	state, ok := entity.State.(domain.Switch)
	if !ok {
		if stateMap, ok := entity.State.(map[string]interface{}); ok {
			if power, ok := stateMap["power"].(bool); ok {
				state.Power = power
			}
		}
	}

	mutate(&state)
	entity.State = state

	if err := a.store.Save(entity); err != nil {
		log.Printf("plugin-kasa: failed to update state for %s: %v", entity.ID, err)
	}
}
