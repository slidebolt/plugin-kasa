// Package logic implements the Kasa bundle adapter. It discovers TP-Link Kasa
// smart plugs, power strips, and smart bulbs via UDP broadcast (port 9999),
// registers them as framework instances, and services per-device command events
// (power, brightness, color temperature, colour).
package logic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lms-io/module-framework/pkg/framework"
)

// ModuleHandler is the LifecycleHandler implementation for the Kasa bundle.
// It manages one persistent goroutine per device that polls sysinfo and
// processes command events.
type ModuleHandler struct {
	activeConns map[string]context.CancelFunc
	mu          sync.Mutex
	api         framework.ModuleAPI
	cancel      context.CancelFunc
	client      KasaClient
	initialized bool
}

// NewModuleHandler creates an empty ModuleHandler ready for Init.
func NewModuleHandler() *ModuleHandler {
	return &ModuleHandler{
		activeConns: make(map[string]context.CancelFunc),
	}
}

func (h *ModuleHandler) ValidateConfig(ctx context.Context, config map[string]any) error {
	return nil
}

func (h *ModuleHandler) Init(api_host framework.ModuleAPI) error {
	h.api = api_host
	ctx, cancel := context.WithCancel(api_host.Context())
	h.cancel = cancel

	if constructor, ok := api_host.GetModuleConfig()["_client_constructor"].(ClientConstructor); ok {
		h.client = constructor()
	} else {
		h.client = DefaultConstructor()
	}

	h.api.Info("Kasa Initialization Triggered")
	h.api.SetBundleStatus(framework.BundleStatus{State: framework.StateActive, Message: "Running"})

	// 1. Bootstrap existing instances from disk
	for _, inst := range api_host.GetInstances() {
		ip, _ := inst.Config["ip"].(string)
		if ip != "" {
			h.runDeviceLogic(ctx, inst)
		}
	}

	// 2. Start UDP Discovery Listener
	go func() {
		_, err := h.client.ListenUDP(func(ip string, info KasaSysInfo) {
			go h.register(ctx, ip, info)
		})
		if err != nil {
			h.api.Error("UDP Listener failed: %v", err)
		}
	}()

	// Give the listener a moment to bind to 9999
	time.Sleep(100 * time.Millisecond)

	// 3. Command Listener
	go func() {
		ch := h.api.Listen("commands/" + h.api.ModuleID())
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-ch:
				if ev.Type == "refresh" {
					h.client.SendUDPProbe()
				}
			}
		}
	}()

	return nil
}

func (h *ModuleHandler) OnStartupTick() {
	go func() {
		for i := 0; i < 5; i++ {
			h.client.SendUDPProbe()
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

func (h *ModuleHandler) OnDiscoverTick() {
	h.client.SendUDPProbe()
}

func (h *ModuleHandler) OnHeartbeatTick() {}

func (h *ModuleHandler) Stop() error {
	if h.cancel != nil {
		h.cancel()
	}
	if h.client != nil {
		h.client.Close()
	}
	return nil
}

func (h *ModuleHandler) OnInstanceRegistered(inst framework.InstanceConfig) {
	ip, _ := inst.Config["ip"].(string)
	if strings.TrimSpace(ip) == "" {
		return
	}
	h.mu.Lock()
	ctx := h.api.Context()
	h.mu.Unlock()
	h.runDeviceLogic(ctx, inst)
}

func (h *ModuleHandler) OnInstanceDeleted(id string) {
	h.mu.Lock()
	cancel, ok := h.activeConns[id]
	if ok {
		cancel()
		delete(h.activeConns, id)
	}
	h.mu.Unlock()
}

func (h *ModuleHandler) DiscoverDevice(config map[string]any) {
	id, _ := config["id"].(string)
	ip, _ := config["ip"].(string)
	if id != "" && ip != "" {
		h.mu.Lock()
		ctx := h.api.Context()
		h.mu.Unlock()

		// Find existing or create minimal
		var target framework.InstanceConfig
		found := false
		for _, inst := range h.api.GetInstances() {
			if inst.ID == id {
				target = inst
				found = true
				break
			}
		}
		if !found {
			target = framework.InstanceConfig{
				ID:      id,
				Enabled: true,
				Config:  map[string]any{"ip": ip, "mac": id},
				Meta:    map[string]any{"ip": ip, "mac": id},
			}
		}
		h.runDeviceLogic(ctx, target)
	}
}

func (h *ModuleHandler) register(ctx context.Context, ip string, info KasaSysInfo) {
	baseMac := normalizeMac(info.Mac)
	if len(info.Children) > 0 {
		for _, child := range info.Children {
			id := fmt.Sprintf("%s-%s", baseMac, child.ID)
			childInfo := info
			childInfo.Alias = child.Alias

			rawEntities := RawEntitiesFromSysInfo(childInfo, child.ID)
			entities := EntitiesFromRaw(rawEntities, childInfo, child.ID)
			rawState := RawStateFromSysInfo(childInfo, child.ID)
			entityState := EntityStateFromSysInfo(childInfo, child.ID)

			config := framework.InstanceConfig{
				ID:          id,
				Name:        child.Alias,
				Enabled:     true,
				Config:      map[string]any{"ip": ip, "mac": baseMac, "child_id": child.ID, "name": child.Alias, "model": info.Model},
				RawEntities: rawEntities,
				RawState:    rawState,
				Entities:    entities,
				EntityState: entityState,
				Meta:        map[string]any{"model": info.Model, "mac": baseMac, "ip": ip, "status": "online"},
			}

			err := h.api.RegisterInstance(config)
			if err == nil {
				h.runDeviceLogic(ctx, config)
			}
		}
	} else {
		id := baseMac
		rawEntities := RawEntitiesFromSysInfo(info, "")
		entities := EntitiesFromRaw(rawEntities, info, "")
		rawState := RawStateFromSysInfo(info, "")
		entityState := EntityStateFromSysInfo(info, "")

		config := framework.InstanceConfig{
			ID:          id,
			Name:        info.Alias,
			Enabled:     true,
			Config:      map[string]any{"ip": ip, "mac": id, "name": info.Alias, "model": info.Model, "is_light": info.IsLight()},
			RawEntities: rawEntities,
			RawState:    rawState,
			Entities:    entities,
			EntityState: entityState,
			Meta:        map[string]any{"model": info.Model, "mac": id, "ip": ip, "status": "online"},
		}

		err := h.api.RegisterInstance(config)
		if err == nil {
			h.runDeviceLogic(ctx, config)
		}
	}
}

func normalizeMac(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, ":", "")
	return value
}

func (h *ModuleHandler) runDeviceLogic(parentCtx context.Context, inst framework.InstanceConfig) {
	id := inst.ID
	ip, _ := inst.Config["ip"].(string)
	childID, _ := inst.Config["child_id"].(string)

	h.mu.Lock()
	if cancel, exists := h.activeConns[id]; exists {
		cancel()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	h.activeConns[id] = cancel
	h.mu.Unlock()

	type pendingUpdate struct {
		state   map[string]any
		expires time.Time
	}

	go func() {
		defer func() {
			h.mu.Lock()
			// handled by cancel pattern
			h.mu.Unlock()
		}()

		currentInst := inst
		// Self-heal: if we started with a minimal config, try to get the full one from registry
		if len(currentInst.Entities) == 0 {
			for _, registered := range h.api.GetInstances() {
				if registered.ID == id && len(registered.Entities) > 0 {
					currentInst = registered
					break
				}
			}
		}

		pending := make(map[string]pendingUpdate)
		var pMu sync.Mutex

		ch := h.api.Listen("commands/" + id)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if info, err := h.client.GetSysInfo(ip); err == nil && info != nil {
					update := EntityStateFromSysInfo(*info, childID)

					// Apply pending overrides
					pMu.Lock()
					for entityID, pu := range pending {
						if time.Now().Before(pu.expires) {
							if target, ok := update[entityID]; ok {
								for k, v := range pu.state {
									target[k] = v
								}
							}
						} else {
							delete(pending, entityID)
						}
					}
					pMu.Unlock()

					h.api.UpdateEntityState(id, update)

					if currentInst.Meta == nil {
						currentInst.Meta = make(map[string]any)
					}
					if currentInst.Meta["status"] != "online" {
						currentInst.Meta["status"] = "online"
						h.api.RegisterInstance(currentInst)
					}
				} else {
					if currentInst.Meta == nil {
						currentInst.Meta = make(map[string]any)
					}
					if currentInst.Meta["status"] == "online" {
						h.api.Warn("[%s] Kasa device unreachable at %s", id, ip)
						currentInst.Meta["status"] = "offline"
						h.api.RegisterInstance(currentInst)
					}
				}
			case ev := <-ch:
				// Determine entity ID for this device
				entityID := "power"
				if childID == "" && h.isLight(id) {
					entityID = "light"
				}

				// Handle power state
				if stateVal, ok := ev.Data["power"].(bool); ok {
					state := 0
					if stateVal {
						state = 1
					}
					if err := h.client.SetPower(ip, childID, state); err == nil {

						update := map[string]any{"power": stateVal}
						pMu.Lock()
						pending[entityID] = pendingUpdate{
							state:   update,
							expires: time.Now().Add(5 * time.Second),
						}
						pMu.Unlock()

						h.api.UpdateEntityState(id, map[string]map[string]any{
							entityID: update,
						})

						if currentInst.Meta == nil {
							currentInst.Meta = make(map[string]any)
						}
						if currentInst.Meta["status"] != "online" {
							currentInst.Meta["status"] = "online"
							h.api.RegisterInstance(currentInst)
						}
					} else {
						h.api.Warn("[%s] Failed to send power command to Kasa device at %s: %v", id, ip, err)
						if currentInst.Meta == nil {
							currentInst.Meta = make(map[string]any)
						}
						if currentInst.Meta["status"] == "online" {
							currentInst.Meta["status"] = "offline"
							h.api.RegisterInstance(currentInst)
						}
					}
				}

				// Collect light params for bulb commands
				lightParams := make(map[string]any)
				if val, ok := ev.Data["brightness"].(float64); ok {
					lightParams["brightness"] = int(val)
				}
				if val, ok := ev.Data["color_temp"].(float64); ok {
					lightParams["color_temp"] = int(val)
				}
				if val, ok := ev.Data["hue"].(float64); ok {
					lightParams["hue"] = int(val)
				}
				if val, ok := ev.Data["saturation"].(float64); ok {
					lightParams["saturation"] = int(val)
				}

				if len(lightParams) > 0 {
					if err := h.client.SetLightState(ip, lightParams); err == nil {
						optimistic := make(map[string]any)
						for k, v := range lightParams {
							optimistic[k] = v
						}

						pMu.Lock()
						pending[entityID] = pendingUpdate{
							state:   optimistic,
							expires: time.Now().Add(5 * time.Second),
						}
						pMu.Unlock()

						h.api.UpdateEntityState(id, map[string]map[string]any{
							entityID: optimistic,
						})

						if currentInst.Meta == nil {
							currentInst.Meta = make(map[string]any)
						}
						if currentInst.Meta["status"] != "online" {
							currentInst.Meta["status"] = "online"
							h.api.RegisterInstance(currentInst)
						}
					} else {
						h.api.Warn("[%s] Failed to send light state command to Kasa device at %s: %v", id, ip, err)
						if currentInst.Meta == nil {
							currentInst.Meta = make(map[string]any)
						}
						if currentInst.Meta["status"] == "online" {
							currentInst.Meta["status"] = "offline"
							h.api.RegisterInstance(currentInst)
						}
					}
				}
			}
		}
	}()
}

func (h *ModuleHandler) isLight(id string) bool {
	for _, inst := range h.api.GetInstances() {
		if inst.ID == id {
			v, _ := inst.Config["is_light"].(bool)
			return v
		}
	}
	return false
}
