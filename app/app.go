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

type App struct {
	msg    messenger.Messenger
	store  storage.Storage
	cmds   *messenger.Commands
	subs   []messenger.Subscription
	ctx    context.Context
	cancel context.CancelFunc
	ticker *time.Ticker

	mu      sync.RWMutex
	devices []translate.SysInfo
	ipMap   map[string]string
	seen    map[string]struct{}
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
		subnet = "192.168.88"
	}

	timeout := 400 * time.Millisecond
	if t := os.Getenv("KASA_TIMEOUT_MS"); t != "" {
		if ms, err := strconv.Atoi(t); err == nil {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	devices, err := translate.DiscoverDevices(subnet, timeout)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.devices = devices
	a.mu.Unlock()
	for _, dev := range devices {
		deviceID := translate.MakeDeviceID(dev.Mac)
		a.mu.Lock()
		_, alreadySeen := a.seen[deviceID]
		a.seen[deviceID] = struct{}{}
		a.mu.Unlock()

		deviceType := "kasa_switch"
		if strings.Contains(strings.ToLower(dev.Model), "bulb") ||
			strings.Contains(strings.ToLower(dev.Model), "kl") {
			deviceType = "light"
		}

		entity := domain.Entity{
			ID:       deviceID,
			Plugin:   PluginID,
			DeviceID: deviceID,
			Type:     deviceType,
			Name:     dev.Alias,
			Commands: []string{
				"switch_turn_on", "switch_turn_off", "switch_toggle",
			},
			State: domain.Switch{
				Power: dev.RelayState == 1,
			},
			Meta: map[string]json.RawMessage{
				"model": json.RawMessage(fmt.Sprintf(`"%s"`, dev.Model)),
				"mac":   json.RawMessage(fmt.Sprintf(`"%s"`, dev.Mac)),
			},
		}

		if err := a.store.Save(entity); err != nil {
			log.Printf("plugin-kasa: failed to save entity %s: %v", deviceID, err)
		} else if !alreadySeen {
			log.Printf("plugin-kasa: registered %s (%s)", dev.Alias, deviceID)
		}
	}

	a.refreshIPMappings(subnet)
	return nil
}

func (a *App) refreshIPMappings(subnet string) {
	base := subnet + "."
	for i := 1; i <= 254; i++ {
		ip := base + strconv.Itoa(i)
		go func(targetIP string) {
			if info, err := translate.GetDeviceInfo(targetIP); err == nil {
				deviceID := translate.MakeDeviceID(info.Mac)
				a.mu.Lock()
				a.ipMap[deviceID] = targetIP
				a.mu.Unlock()
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
	ip := a.getDeviceIP(addr.EntityID)
	if ip == "" {
		log.Printf("plugin-kasa: no IP for device %s, attempting refresh", addr.EntityID)
		ip = a.lookupIPFromStorage(addr.EntityID)
		if ip == "" {
			log.Printf("plugin-kasa: still no IP for device %s", addr.EntityID)
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

	switch cmd.(type) {
	case domain.SwitchTurnOn:
		log.Printf("plugin-kasa: switch %s turn_on", addr.Key())
		if err := translate.SetPower(ip, 1); err != nil {
			log.Printf("plugin-kasa: failed to turn on %s: %v", addr.Key(), err)
		} else {
			a.updateSwitchState(entity, func(s *domain.Switch) { s.Power = true })
		}
	case domain.SwitchTurnOff:
		log.Printf("plugin-kasa: switch %s turn_off", addr.Key())
		if err := translate.SetPower(ip, 0); err != nil {
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
			if err := translate.SetPower(ip, newState); err != nil {
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
