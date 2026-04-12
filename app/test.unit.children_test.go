package app

import (
	"encoding/json"
	"testing"

	translate "github.com/slidebolt/plugin-kasa/internal/translate"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	testkit "github.com/slidebolt/sb-testkit"
)

func TestRegisterDiscoveredDevice_SavesChildOutlets(t *testing.T) {
	env := testkit.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	a := New()
	a.store = env.Storage()
	a.seen = make(map[string]struct{})
	a.entityChildIDs = make(map[string]string)

	dev := translate.SysInfo{
		Alias:    "TP-LINK_Smart Plug_0A49",
		Model:    "EP40(US)",
		Mac:      "28:87:BA:95:0A:49",
		DeviceID: "80067FCB6D318DBCDED89309B7249B791FEFC423",
		Children: []translate.ChildInfo{
			{ID: "80067FCB6D318DBCDED89309B7249B791FEFC42300", Alias: "deck lights ", RelayState: 1},
			{ID: "80067FCB6D318DBCDED89309B7249B791FEFC42301", Alias: "deck holiday lights", RelayState: 0},
		},
	}

	a.registerDiscoveredDevice(dev)

	if _, err := env.Storage().Get(domain.EntityKey{Plugin: PluginID, DeviceID: "kasa-2887ba950a49", ID: "kasa-2887ba950a49"}); err == nil {
		t.Fatal("unexpected aggregate entity for multi-outlet device")
	}

	child0 := mustGetEntity(t, env, "kasa-2887ba950a49", "outlet-00")
	if child0.Name != "deck lights" {
		t.Fatalf("child0 name = %q, want deck lights", child0.Name)
	}
	child0State, ok := child0.State.(domain.Switch)
	if !ok || !child0State.Power {
		t.Fatalf("child0 state = %#v, want power true", child0.State)
	}
	if got := a.getEntityChildID("kasa-2887ba950a49", "outlet-00"); got != "80067FCB6D318DBCDED89309B7249B791FEFC42300" {
		t.Fatalf("child0 runtime child id = %q", got)
	}

	child1 := mustGetEntity(t, env, "kasa-2887ba950a49", "outlet-01")
	if child1.Name != "deck holiday lights" {
		t.Fatalf("child1 name = %q, want deck holiday lights", child1.Name)
	}
	child1State, ok := child1.State.(domain.Switch)
	if !ok || child1State.Power {
		t.Fatalf("child1 state = %#v, want power false", child1.State)
	}
}

func TestHandleCommand_ChildOutletTargetsChildRelay(t *testing.T) {
	env := testkit.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	a := New()
	a.store = env.Storage()
	a.ipMap = map[string]string{"kasa-2887ba950a49": "192.0.2.10"}
	a.entityChildIDs = map[string]string{
		"kasa-2887ba950a49.outlet-01": "80067FCB6D318DBCDED89309B7249B791FEFC42301",
	}

	child := domain.Entity{
		ID:       "outlet-01",
		Plugin:   PluginID,
		DeviceID: "kasa-2887ba950a49",
		Type:     "kasa_switch",
		Name:     "deck holiday lights",
		Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
		State:    domain.Switch{Power: false},
	}
	if err := env.Storage().Save(child); err != nil {
		t.Fatalf("save child: %v", err)
	}

	origSetPower := setPower
	origSetChildPower := setChildPower
	t.Cleanup(func() {
		setPower = origSetPower
		setChildPower = origSetChildPower
	})

	var calledParent bool
	var gotIP, gotChildID string
	var gotState int
	setPower = func(ip string, state int) error {
		calledParent = true
		return nil
	}
	setChildPower = func(ip, childID string, state int) error {
		gotIP, gotChildID, gotState = ip, childID, state
		return nil
	}

	a.handleCommand(messenger.Address{
		Plugin:   PluginID,
		DeviceID: "kasa-2887ba950a49",
		EntityID: "outlet-01",
	}, domain.SwitchTurnOn{})

	if calledParent {
		t.Fatal("setPower called for outlet entity")
	}
	if gotIP != "192.0.2.10" || gotChildID != "80067FCB6D318DBCDED89309B7249B791FEFC42301" || gotState != 1 {
		t.Fatalf("setChildPower args = (%q, %q, %d)", gotIP, gotChildID, gotState)
	}

	got := mustGetEntity(t, env, "kasa-2887ba950a49", "outlet-01")
	state, ok := got.State.(domain.Switch)
	if !ok || !state.Power {
		t.Fatalf("updated state = %#v, want power true", got.State)
	}
}

func mustGetEntity(t *testing.T, env *testkit.TestEnv, deviceID, entityID string) domain.Entity {
	t.Helper()
	raw, err := env.Storage().Get(domain.EntityKey{Plugin: PluginID, DeviceID: deviceID, ID: entityID})
	if err != nil {
		t.Fatalf("get %s.%s: %v", deviceID, entityID, err)
	}
	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("unmarshal %s.%s: %v", deviceID, entityID, err)
	}
	return entity
}
