//go:build integration

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	app "github.com/slidebolt/plugin-kasa/app"
	translate "github.com/slidebolt/plugin-kasa/internal/translate"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	testkit "github.com/slidebolt/sb-testkit"
)

func loadEnvLocal(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../.env.local")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			t.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
}

// TestDiscovery_FindDevices broadcasts on the local subnet to discover Kasa
// smart devices and fails if none are found. Reads KASA_SUBNET from .env.local.
//
// Run: go test -tags integration -v -run TestDiscovery_FindDevices ./cmd/plugin-kasa/
func TestDiscovery_FindDevices(t *testing.T) {
	loadEnvLocal(t)

	subnet := os.Getenv("KASA_SUBNET")
	if subnet == "" {
		t.Fatal("KASA_SUBNET not set — add it to .env.local")
	}
	timeoutMs := 400
	if v := os.Getenv("KASA_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			timeoutMs = n
		}
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	t.Logf("scanning %s.* for Kasa devices (timeout %v)...", subnet, timeout)

	devices, err := translate.DiscoverDevices(subnet, timeout)
	if err != nil {
		t.Fatalf("discovery error: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("no Kasa devices found on subnet " + subnet)
	}

	t.Logf("found %d device(s):", len(devices))
	for _, d := range devices {
		t.Logf("  %-25s %-15s %s", d.Alias, d.Model, d.Mac)
	}
}

func TestDiscovery_RegistersEP40OutletsAsEntities(t *testing.T) {
	loadEnvLocal(t)

	subnet := os.Getenv("KASA_SUBNET")
	if subnet == "" {
		t.Fatal("KASA_SUBNET not set -- add it to .env.local")
	}

	t.Setenv("KASA_SUBNET", subnet)
	_, env := startLocalPlugin(t)

	entries, err := env.Storage().Search("plugin-kasa.kasa-2887ba950a49.*")
	if err != nil {
		t.Fatalf("search kasa EP40 entities: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("EP40 entities = %d, want 2", len(entries))
	}

	names := map[string]bool{}
	for _, entry := range entries {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			t.Fatalf("unmarshal entity %s: %v", entry.Key, err)
		}
		names[entity.Name] = true
		if entity.DeviceID != "kasa-2887ba950a49" {
			t.Fatalf("%s deviceID = %q, want kasa-2887ba950a49", entry.Key, entity.DeviceID)
		}
	}

	if !names["deck lights"] || !names["deck holiday lights"] {
		t.Fatalf("unexpected EP40 outlet names: %+v", names)
	}
}

func TestIntegration_TargetStripRegistersNamedOutlets(t *testing.T) {
	loadEnvLocal(t)

	subnet := os.Getenv("KASA_SUBNET")
	if subnet == "" {
		t.Fatal("KASA_SUBNET not set -- add it to .env.local")
	}
	targetIP := strings.TrimSpace(os.Getenv("KASA_TEST_DEVICE_IP"))
	if targetIP == "" {
		t.Skip("set KASA_TEST_DEVICE_IP to verify a specific real device")
	}

	info, err := translate.GetDeviceInfo(targetIP)
	if err != nil {
		t.Fatalf("get device info for %s: %v", targetIP, err)
	}

	expectedDeviceID := strings.TrimSpace(os.Getenv("KASA_TEST_DEVICE_ID"))
	if expectedDeviceID == "" {
		expectedDeviceID = "kasa-2887ba950a49"
	}

	deviceID := translate.MakeDeviceID(info.Mac)
	if deviceID != expectedDeviceID {
		t.Fatalf("deviceID = %q, want %q", deviceID, expectedDeviceID)
	}

	expectedNames := []string{"deck lights", "deck holiday lights"}
	if raw := strings.TrimSpace(os.Getenv("KASA_TEST_EXPECTED_NAMES")); raw != "" {
		expectedNames = splitCSV(raw)
	}

	_, env := startLocalPlugin(t)
	entities := waitForEntities(t, env, deviceID, len(expectedNames))

	gotByName := make(map[string]domain.Entity)
	for _, entry := range entities {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			t.Fatalf("unmarshal %s: %v", entry.Key, err)
		}
		gotByName[entity.Name] = entity
	}

	for index, expectedName := range expectedNames {
		entity, ok := gotByName[expectedName]
		if !ok {
			t.Fatalf("missing entity %q in %+v", expectedName, mapsKeys(gotByName))
		}
		wantEntityID := fmt.Sprintf("outlet-%02d", index)
		if entity.ID != wantEntityID {
			t.Fatalf("%s entity ID = %q, want %q", expectedName, entity.ID, wantEntityID)
		}
	}
}

func TestIntegration_TargetStripOutletRoundTrip(t *testing.T) {
	loadEnvLocal(t)

	if !envBool("KASA_TEST_ALLOW_MUTATION") {
		t.Skip("set KASA_TEST_ALLOW_MUTATION=1 to run live outlet command test")
	}

	subnet := os.Getenv("KASA_SUBNET")
	if subnet == "" {
		t.Fatal("KASA_SUBNET not set -- add it to .env.local")
	}
	targetIP := strings.TrimSpace(os.Getenv("KASA_TEST_DEVICE_IP"))
	if targetIP == "" {
		t.Fatal("KASA_TEST_DEVICE_IP not set")
	}

	info, err := translate.GetDeviceInfo(targetIP)
	if err != nil {
		t.Fatalf("get device info for %s: %v", targetIP, err)
	}

	targetName := strings.TrimSpace(os.Getenv("KASA_TEST_OUTLET_NAME"))
	if targetName == "" {
		targetName = "deck holiday lights"
	}

	child, ok := childByAlias(*info, targetName)
	if !ok {
		t.Fatalf("child %q not found on %s", targetName, targetIP)
	}

	deviceID := translate.MakeDeviceID(info.Mac)
	entityID := translate.MakeChildEntityID(info.DeviceID, child.ID)
	originalPower := child.RelayState == 1
	targetPower := !originalPower

	t.Cleanup(func() {
		restoreState := 0
		if originalPower {
			restoreState = 1
		}
		if err := translate.SetChildPower(targetIP, child.ID, restoreState); err != nil {
			t.Errorf("restore %s: %v", child.Alias, err)
			return
		}
		if err := waitForChildPower(targetIP, child.ID, originalPower, 8*time.Second); err != nil {
			t.Errorf("restore %s state: %v", child.Alias, err)
		}
	})

	_, env := startLocalPlugin(t)
	entity := waitForEntity(t, env, deviceID, entityID)
	cmds := messenger.NewCommands(env.Messenger(), domain.LookupCommand)

	var cmd messenger.Action = domain.SwitchTurnOff{}
	if targetPower {
		cmd = domain.SwitchTurnOn{}
	}
	if err := cmds.Send(entity, cmd); err != nil {
		t.Fatalf("send command: %v", err)
	}

	if err := waitForChildPower(targetIP, child.ID, targetPower, 8*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForStoredPower(env, deviceID, entityID, targetPower, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func startLocalPlugin(t *testing.T) (*app.App, *testkit.TestEnv) {
	t.Helper()

	env := testkit.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	p := app.New()
	deps := map[string]json.RawMessage{
		"messenger": env.MessengerPayload(),
		"storage":   env.StoragePayload(),
	}
	if _, err := p.OnStart(deps); err != nil {
		t.Fatalf("plugin OnStart: %v", err)
	}
	t.Cleanup(func() { _ = p.OnShutdown() })
	return p, env
}

func waitForEntities(t *testing.T, env *testkit.TestEnv, deviceID string, want int) []testkitEntry {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := env.Storage().Search("plugin-kasa." + deviceID + ".*")
		if err == nil && len(entries) >= want {
			out := make([]testkitEntry, len(entries))
			for i, entry := range entries {
				out[i] = testkitEntry{Key: entry.Key, Data: entry.Data}
			}
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}

	entries, err := env.Storage().Search("plugin-kasa." + deviceID + ".*")
	if err != nil {
		t.Fatalf("search %s: %v", deviceID, err)
	}
	t.Fatalf("entities for %s = %d, want at least %d", deviceID, len(entries), want)
	return nil
}

func waitForEntity(t *testing.T, env *testkit.TestEnv, deviceID, entityID string) domain.Entity {
	t.Helper()

	key := domain.EntityKey{Plugin: "plugin-kasa", DeviceID: deviceID, ID: entityID}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := env.Storage().Get(key)
		if err == nil {
			var entity domain.Entity
			if err := json.Unmarshal(raw, &entity); err != nil {
				t.Fatalf("unmarshal %s: %v", key, err)
			}
			return entity
		}
		time.Sleep(100 * time.Millisecond)
	}

	raw, err := env.Storage().Get(key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("unmarshal %s: %v", key, err)
	}
	return entity
}

func waitForStoredPower(env *testkit.TestEnv, deviceID, entityID string, want bool, timeout time.Duration) error {
	key := domain.EntityKey{Plugin: "plugin-kasa", DeviceID: deviceID, ID: entityID}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := env.Storage().Get(key)
		if err == nil {
			var entity domain.Entity
			if err := json.Unmarshal(raw, &entity); err == nil {
				if state, ok := entity.State.(domain.Switch); ok && state.Power == want {
					return nil
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("stored state for %s.%s did not reach power=%t", deviceID, entityID, want)
}

func waitForChildPower(ip, childID string, want bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := translate.GetDeviceInfo(ip)
		if err == nil {
			for _, child := range info.Children {
				if child.ID == childID && (child.RelayState == 1) == want {
					return nil
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("child %s on %s did not reach power=%t", childID, ip, want)
}

func childByAlias(info translate.SysInfo, alias string) (translate.ChildInfo, bool) {
	want := strings.TrimSpace(strings.ToLower(alias))
	for _, child := range info.Children {
		if strings.TrimSpace(strings.ToLower(child.Alias)) == want {
			return child, true
		}
	}
	return translate.ChildInfo{}, false
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func mapsKeys(values map[string]domain.Entity) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

type testkitEntry struct {
	Key  string
	Data []byte
}
