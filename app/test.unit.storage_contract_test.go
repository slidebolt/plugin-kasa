package app_test

import (
	"encoding/json"
	"reflect"
	"testing"

	kasaapp "github.com/slidebolt/plugin-kasa/app"
	domain "github.com/slidebolt/sb-domain"
	managersdk "github.com/slidebolt/sb-manager-sdk"
)

func TestStorageContract_KasaEntityRoundTrips(t *testing.T) {
	env := managersdk.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	entity := domain.Entity{
		ID:       "kasa-2887ba559988",
		Plugin:   kasaapp.PluginID,
		DeviceID: "kasa-2887ba559988",
		Type:     "kasa_switch",
		Name:     "Kasa Plug",
		Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
		State:    domain.Switch{Power: true},
	}
	if err := env.Storage().Save(entity); err != nil {
		t.Fatalf("save entity: %v", err)
	}

	raw, err := env.Storage().Get(domain.EntityKey{Plugin: kasaapp.PluginID, DeviceID: "kasa-2887ba559988", ID: "kasa-2887ba559988"})
	if err != nil {
		t.Fatalf("get entity: %v", err)
	}
	var got domain.Entity
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.Commands, entity.Commands) {
		t.Fatalf("commands = %v, want %v", got.Commands, entity.Commands)
	}
	if state, ok := got.State.(domain.Switch); !ok || !state.Power {
		t.Fatalf("state = %#v", got.State)
	}
}
