//go:build local



package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/slidebolt/sdk-integration-testing"
	"github.com/slidebolt/sdk-types"
)

const kasaLocalPluginID = "plugin-kasa"

// ── Test 01: Discovery ────────────────────────────────────────────────────────

func TestKasaLocal_01_Discovery(t *testing.T) {
	requireKasaLocalEnv(t)

	s := integrationtesting.New(t, "github.com/slidebolt/plugin-kasa", ".")
	s.RequirePlugin(kasaLocalPluginID)

	devices := waitForKasaDevices(t, s, 30*time.Second)
	t.Logf("discovered %d Kasa device(s)", len(devices))

	for _, dev := range devices {
		t.Logf("  device: id=%s source_name=%q local_name=%q", dev.ID, dev.SourceName, dev.LocalName)
	}
}

// ── Test 02: Entity validation ────────────────────────────────────────────────

func TestKasaLocal_02_DeviceEntities(t *testing.T) {
	requireKasaLocalEnv(t)

	s := integrationtesting.New(t, "github.com/slidebolt/plugin-kasa", ".")
	s.RequirePlugin(kasaLocalPluginID)

	devices := waitForKasaDevices(t, s, 30*time.Second)
	if len(devices) == 0 {
		t.Skip("no devices discovered")
	}

	for _, dev := range devices {
		var entities []types.Entity
		path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities", kasaLocalPluginID, dev.ID)
		if err := s.GetJSON(path, &entities); err != nil {
			t.Errorf("get entities for %s: %v", dev.ID, err)
			continue
		}

		t.Logf("device %q (%s): %d entities", dev.LocalName, dev.ID, len(entities))
		for _, ent := range entities {
			t.Logf("  entity: id=%s domain=%s actions=%v", ent.ID, ent.Domain, ent.Actions)
		}

		if len(entities) == 0 {
			t.Errorf("device %s has no entities", dev.ID)
			continue
		}

		// Every Kasa device should have at least one switch or light entity.
		hasPower := false
		for _, ent := range entities {
			if ent.Domain == "switch" || ent.Domain == "light" {
				hasPower = true
				hasOn := slices.Contains(ent.Actions, "turn_on")
				hasOff := slices.Contains(ent.Actions, "turn_off")
				if !hasOn || !hasOff {
					t.Errorf("device %s entity %s missing turn_on/turn_off actions: %v", dev.ID, ent.ID, ent.Actions)
				}
			}
		}
		if !hasPower {
			t.Errorf("device %s has no switch/light entity", dev.ID)
		}
	}
}

// ── Test 03: Multi-outlet grouping ────────────────────────────────────────────

func TestKasaLocal_03_MultiOutletGrouping(t *testing.T) {
	requireKasaLocalEnv(t)

	s := integrationtesting.New(t, "github.com/slidebolt/plugin-kasa", ".")
	s.RequirePlugin(kasaLocalPluginID)

	devices := waitForKasaDevices(t, s, 30*time.Second)

	// Group devices by their MAC prefix (everything before the first "-").
	byMac := make(map[string][]types.Device)
	for _, dev := range devices {
		mac := strings.SplitN(dev.ID, "-", 2)[0]
		byMac[mac] = append(byMac[mac], dev)
	}

	foundMulti := false
	for mac, group := range byMac {
		if len(group) > 1 {
			foundMulti = true
			t.Logf("multi-outlet device (mac=%s): %d outlets", mac, len(group))
			for _, dev := range group {
				t.Logf("  outlet: id=%s local_name=%q", dev.ID, dev.LocalName)
			}

			// Each outlet must have a distinct local name.
			names := map[string]bool{}
			for _, dev := range group {
				if names[dev.LocalName] {
					t.Errorf("duplicate local_name %q on mac %s", dev.LocalName, mac)
				}
				names[dev.LocalName] = true
			}
		}
	}

	if !foundMulti {
		t.Log("no multi-outlet devices found (EP40 not present on network)")
	}
}

// ── Test 04: Power toggle (turn off then on) ──────────────────────────────────

func TestKasaLocal_04_PowerToggle(t *testing.T) {
	requireKasaLocalEnv(t)

	s := integrationtesting.New(t, "github.com/slidebolt/plugin-kasa", ".")
	s.RequirePlugin(kasaLocalPluginID)

	devices := waitForKasaDevices(t, s, 30*time.Second)
	if len(devices) == 0 {
		t.Skip("no devices discovered")
	}

	// Pick the first switch/light device that has a controllable entity.
	var targetDev *types.Device
	var targetEnt *types.Entity
	for i := range devices {
		dev := &devices[i]
		var entities []types.Entity
		path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities", kasaLocalPluginID, dev.ID)
		if err := s.GetJSON(path, &entities); err != nil {
			continue
		}
		for j := range entities {
			ent := &entities[j]
			if ent.Domain == "switch" || ent.Domain == "light" {
				targetDev = dev
				entCopy := *ent
				targetEnt = &entCopy
				break
			}
		}
		if targetDev != nil {
			break
		}
	}
	if targetDev == nil {
		t.Skip("no controllable switch/light entity found")
	}
	t.Logf("testing power toggle on device=%q entity=%s domain=%s", targetDev.LocalName, targetEnt.ID, targetEnt.Domain)

	// Turn off.
	sendKasaCommand(t, s, targetDev.ID, targetEnt.ID, "turn_off")
	t.Log("sent turn_off — waiting for reported state power=false")
	waitForPowerState(t, s, targetDev.ID, targetEnt.ID, false, 10*time.Second)
	t.Log("confirmed power=false")

	time.Sleep(300 * time.Millisecond)

	// Turn on — restore original state.
	sendKasaCommand(t, s, targetDev.ID, targetEnt.ID, "turn_on")
	t.Log("sent turn_on — waiting for reported state power=true")
	waitForPowerState(t, s, targetDev.ID, targetEnt.ID, true, 10*time.Second)
	t.Log("confirmed power=true")
}

// ── Test 05: Discovery and restart ───────────────────────────────────────────

func TestKasaLocal_05_DiscoveryAndRestart(t *testing.T) {
	requireKasaLocalEnv(t)

	s := integrationtesting.New(t, "github.com/slidebolt/plugin-kasa", ".")
	s.RequirePlugin(kasaLocalPluginID)

	devicesBefore := waitForKasaDevices(t, s, 30*time.Second)
	idsBefore := kasaDeviceIDs(devicesBefore)
	t.Logf("before restart: %d device(s) — %v", len(idsBefore), idsBefore)

	s.Restart()
	s.RequirePlugin(kasaLocalPluginID)

	devicesAfter := waitForKasaDevices(t, s, 30*time.Second)
	idsAfter := kasaDeviceIDs(devicesAfter)
	t.Logf("after restart:  %d device(s) — %v", len(idsAfter), idsAfter)

	slices.Sort(idsBefore)
	slices.Sort(idsAfter)
	if !slices.Equal(idsBefore, idsAfter) {
		t.Fatalf("device set changed across restart: before=%v after=%v", idsBefore, idsAfter)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func requireKasaLocalEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("KASA_STATIC_IPS") == "" && os.Getenv("KASA_DISCOVERY_SUBNETS") == "" {
		t.Skip("KASA_STATIC_IPS or KASA_DISCOVERY_SUBNETS not set; source plugin-kasa/.env.local before running local tests")
	}
}

// waitForKasaDevices polls until at least one non-core device appears.
func waitForKasaDevices(t *testing.T, s *integrationtesting.Suite, timeout time.Duration) []types.Device {
	t.Helper()
	var all []types.Device
	ok := s.WaitFor(timeout, func() bool {
		if err := s.GetJSON(fmt.Sprintf("/api/plugins/%s/devices", kasaLocalPluginID), &all); err != nil {
			return false
		}
		return len(kasaNonCoreDevices(all)) > 0
	})
	if !ok {
		t.Fatalf("no Kasa devices discovered within %s (check KASA_STATIC_IPS or KASA_DISCOVERY_SUBNETS)", timeout)
	}
	return kasaNonCoreDevices(all)
}

// kasaNonCoreDevices filters out the core plugin device.
func kasaNonCoreDevices(devices []types.Device) []types.Device {
	coreID := types.CoreDeviceID(kasaLocalPluginID)
	out := devices[:0:0]
	for _, dev := range devices {
		if dev.ID == coreID {
			continue
		}
		out = append(out, dev)
	}
	return out
}

func kasaDeviceIDs(devices []types.Device) []string {
	ids := make([]string, 0, len(devices))
	for _, dev := range devices {
		ids = append(ids, dev.ID)
	}
	slices.Sort(ids)
	return ids
}

// sendKasaCommand POSTs a command and asserts it is accepted (202).
func sendKasaCommand(t *testing.T, s *integrationtesting.Suite, deviceID, entityID, action string) types.CommandStatus {
	t.Helper()
	path := fmt.Sprintf("%s/api/plugins/%s/devices/%s/entities/%s/commands",
		s.APIURL(), kasaLocalPluginID, deviceID, entityID)
	body, _ := json.Marshal(map[string]string{"type": action})
	resp, err := http.Post(path, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST command %s: %v", action, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST command %s: expected 202, got %d", action, resp.StatusCode)
	}
	var status types.CommandStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode command status: %v", err)
	}
	return status
}

// waitForPowerState polls the entity's reported state until the canonical power state matches.
func waitForPowerState(t *testing.T, s *integrationtesting.Suite, deviceID, entityID string, wantOn bool, timeout time.Duration) {
	t.Helper()
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities", kasaLocalPluginID, deviceID)
	ok := s.WaitFor(timeout, func() bool {
		var entities []types.Entity
		if err := s.GetJSON(path, &entities); err != nil {
			return false
		}
		for _, ent := range entities {
			if ent.ID != entityID {
				continue
			}
			if len(ent.Data.Reported) == 0 {
				return false
			}
			var state map[string]any
			if err := json.Unmarshal(ent.Data.Reported, &state); err != nil {
				return false
			}
			power, ok := state["power"].(bool)
			return ok && power == wantOn
		}
		return false
	})
	if !ok {
		t.Fatalf("entity %s/%s never reached power=%v within %s", deviceID, entityID, wantOn, timeout)
	}
}
