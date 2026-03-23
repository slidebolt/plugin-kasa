// Unit tests for plugin-kasa.
//
// Test layer philosophy:
//   Unit tests (this file): pure domain logic, cross-entity behavior,
//     and custom entity type registration. Things that don't express
//     well as BDD scenarios or that test infrastructure capabilities
//     across multiple entity types simultaneously.
//
//   BDD tests (features/*.feature, -tags bdd): per-entity behavioral
//     contract. One feature file per entity type. These are the
//     source of truth for what a plugin promises to support.
//
// Run:
//   go test ./...              - unit tests only
//   go test -tags bdd ./...    - unit tests + BDD scenarios

package main

import (
	"encoding/json"
	"testing"
	"time"

	translate "github.com/slidebolt/plugin-kasa/internal/translate"
	domain "github.com/slidebolt/sb-domain"
	managersdk "github.com/slidebolt/sb-manager-sdk"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
)

// ==========================================================================
// Test helpers
// ==========================================================================

func env(t *testing.T) (*managersdk.TestEnv, storage.Storage, *messenger.Commands) {
	t.Helper()
	e := managersdk.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	cmds := messenger.NewCommands(e.Messenger(), domain.LookupCommand)
	return e, e.Storage(), cmds
}

func saveEntity(t *testing.T, store storage.Storage, plugin, device, id, typ, name string, state any) domain.Entity {
	t.Helper()
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: typ, Name: name, State: state,
	}
	if err := store.Save(e); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	return e
}

func getEntity(t *testing.T, store storage.Storage, plugin, device, id string) domain.Entity {
	t.Helper()
	raw, err := store.Get(domain.EntityKey{Plugin: plugin, DeviceID: device, ID: id})
	if err != nil {
		t.Fatalf("get %s.%s.%s: %v", plugin, device, id, err)
	}
	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return entity
}

func queryByType(t *testing.T, store storage.Storage, typ string) []storage.Entry {
	t.Helper()
	entries, err := store.Query(storage.Query{
		Where: []storage.Filter{{Field: "type", Op: storage.Eq, Value: typ}},
	})
	if err != nil {
		t.Fatalf("query type=%s: %v", typ, err)
	}
	return entries
}

func sendAndReceive(t *testing.T, cmds *messenger.Commands, entity domain.Entity, cmd any, pattern string) any {
	t.Helper()
	done := make(chan any, 1)
	cmds.Receive(pattern, func(addr messenger.Address, c any) {
		done <- c
	})
	if err := cmds.Send(entity, cmd.(messenger.Action)); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
		return nil
	}
}

// ==========================================================================
// Switch Commands
// ==========================================================================

func TestSwitch_TurnOn(t *testing.T) {
	_, store, cmds := env(t)
	saveEntity(t, store, "plugin-kasa", "kasa-aabbccdd", "kasa-aabbccdd", "kasa_switch", "Test Plug", domain.Switch{Power: false})

	entity := domain.Entity{ID: "kasa-aabbccdd", Plugin: "plugin-kasa", DeviceID: "kasa-aabbccdd", Type: "kasa_switch"}
	got := sendAndReceive(t, cmds, entity, domain.SwitchTurnOn{}, "plugin-kasa.>")
	cmd, ok := got.(domain.SwitchTurnOn)
	if !ok {
		t.Fatalf("type: got %T, want SwitchTurnOn", got)
	}
	_ = cmd
}

func TestSwitch_TurnOff(t *testing.T) {
	_, store, cmds := env(t)
	saveEntity(t, store, "plugin-kasa", "kasa-aabbccdd", "kasa-aabbccdd", "kasa_switch", "Test Plug", domain.Switch{Power: true})

	entity := domain.Entity{ID: "kasa-aabbccdd", Plugin: "plugin-kasa", DeviceID: "kasa-aabbccdd", Type: "kasa_switch"}
	got := sendAndReceive(t, cmds, entity, domain.SwitchTurnOff{}, "plugin-kasa.>")
	cmd, ok := got.(domain.SwitchTurnOff)
	if !ok {
		t.Fatalf("type: got %T, want SwitchTurnOff", got)
	}
	_ = cmd
}

func TestSwitch_Toggle(t *testing.T) {
	_, store, cmds := env(t)
	saveEntity(t, store, "plugin-kasa", "kasa-aabbccdd", "kasa-aabbccdd", "kasa_switch", "Test Plug", domain.Switch{Power: true})

	entity := domain.Entity{ID: "kasa-aabbccdd", Plugin: "plugin-kasa", DeviceID: "kasa-aabbccdd", Type: "kasa_switch"}
	got := sendAndReceive(t, cmds, entity, domain.SwitchToggle{}, "plugin-kasa.>")
	cmd, ok := got.(domain.SwitchToggle)
	if !ok {
		t.Fatalf("type: got %T, want SwitchToggle", got)
	}
	_ = cmd
}

// ==========================================================================
// Custom entity: Kasa device
// ==========================================================================

func TestCustom_KasaSwitch_SaveGetHydrate(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "plugin-kasa", "kasa-aabbccdd", "kasa-aabbccdd", "kasa_switch", "Living Room Plug",
		domain.Switch{Power: true})

	got := getEntity(t, store, "plugin-kasa", "kasa-aabbccdd", "kasa-aabbccdd")
	state, ok := got.State.(domain.Switch)
	if !ok {
		t.Fatalf("state type: got %T, want Switch", got.State)
	}
	if !state.Power {
		t.Errorf("expected power to be true, got false")
	}
}

func TestCustom_KasaSwitch_QueryByType(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "plugin-kasa", "kasa-001", "kasa-001", "kasa_switch", "Plug 1", domain.Switch{Power: true})
	saveEntity(t, store, "plugin-kasa", "kasa-002", "kasa-002", "kasa_switch", "Plug 2", domain.Switch{Power: false})
	saveEntity(t, store, "test", "dev1", "switch01", "switch", "Regular Switch", domain.Switch{Power: true})

	entries := queryByType(t, store, "kasa_switch")
	if len(entries) != 2 {
		t.Fatalf("kasa_switches: got %d, want 2", len(entries))
	}
}

// ==========================================================================
// Discovery helpers
// ==========================================================================

func TestDiscovery_MakeDeviceID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"aa:bb:cc:dd:ee:ff", "kasa-aabbccddeeff"},
		{"AA-BB-CC-DD-EE-FF", "kasa-aabbccddeeff"},
		{"aabbccddeeff", "kasa-aabbccddeeff"},
		{"", "kasa-unknown"},
	}

	for _, tc := range tests {
		got := translate.MakeDeviceID(tc.input)
		if got != tc.expected {
			t.Errorf("makeDeviceID(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestEncryption_XOR(t *testing.T) {
	plaintext := `{"system":{"get_sysinfo":null}}`
	encrypted := translate.XOREncrypt(plaintext)
	decrypted := translate.XORDecrypt(encrypted)

	if decrypted != plaintext {
		t.Errorf("xor round-trip failed: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryption_XORWithHeader(t *testing.T) {
	plaintext := `{"system":{"set_relay_state":{"state":1}}}`
	data := translate.EncryptWithHeader(plaintext)

	// Should have 4-byte length header + encrypted payload
	if len(data) < 4 {
		t.Fatalf("encrypted data too short: %d bytes", len(data))
	}

	// Verify we can read the length
	length := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if length != uint32(len(data)-4) {
		t.Errorf("length mismatch: header says %d, actual payload is %d", length, len(data)-4)
	}
}
