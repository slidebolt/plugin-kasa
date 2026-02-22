package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lms-io/module-framework/pkg/framework"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
)

func TestFixturePlugBasic(t *testing.T) {
	info := loadSysInfo(t, "plug_basic.json")

	raw := logic.RawEntitiesFromSysInfo(info, "")
	if len(raw) != 1 || raw[0].Kind != "kasa.relay" {
		t.Fatalf("expected 1 raw relay, got %+v", raw)
	}

	entities := logic.EntitiesFromRaw(raw, info, "")
	if len(entities) != 1 || entities[0].ID != "power" {
		t.Fatalf("expected power entity, got %+v", entities)
	}

	state := logic.EntityStateFromSysInfo(info, "")
	if state["power"]["power"] != true {
		t.Fatalf("expected power state true, got %+v", state["power"])
	}
}

func TestFixturePowerStripChildren(t *testing.T) {
	info := loadSysInfo(t, "power_strip_children.json")

	if len(info.Children) < 2 {
		t.Fatalf("expected children in fixture, got %+v", info.Children)
	}

	childID := info.Children[1].ID
	raw := logic.RawEntitiesFromSysInfo(info, childID)
	if len(raw) != 1 || raw[0].ID != childID {
		t.Fatalf("expected child raw power, got %+v", raw)
	}

	entities := logic.EntitiesFromRaw(raw, info, childID)
	if len(entities) != 1 || entities[0].ID != "power" {
		t.Fatalf("expected child power entity, got %+v", entities)
	}

	state := logic.EntityStateFromSysInfo(info, childID)
	if state["power"]["power"] != false {
		t.Fatalf("expected child state false, got %+v", state["power"])
	}
}

func TestFixtureBulbBrightness(t *testing.T) {
	info := loadSysInfo(t, "bulb_brightness.json")

	raw := logic.RawEntitiesFromSysInfo(info, "")
	if len(raw) != 2 {
		t.Fatalf("expected power+brightness, got %+v", raw)
	}

	entity := singleEntity(t, info, raw)
	if _, ok := entity.Capabilities["brightness"]; !ok {
		t.Fatalf("expected brightness capability, got %+v", entity.Capabilities)
	}
	if _, ok := entity.Capabilities["color_temp"]; ok {
		t.Fatalf("did not expect color_temp capability")
	}
	if _, ok := entity.Capabilities["color"]; ok {
		t.Fatalf("did not expect color capability")
	}

	state := logic.EntityStateFromSysInfo(info, "")
	if state["light"]["brightness"] != 60 {
		t.Fatalf("expected brightness 60, got %+v", state["light"])
	}
}

func TestFixtureBulbTemp(t *testing.T) {
	info := loadSysInfo(t, "bulb_temp.json")

	raw := logic.RawEntitiesFromSysInfo(info, "")
	if len(raw) != 3 {
		t.Fatalf("expected power+brightness+temp, got %+v", raw)
	}

	entity := singleEntity(t, info, raw)
	if _, ok := entity.Capabilities["color_temp"]; !ok {
		t.Fatalf("expected color_temp capability, got %+v", entity.Capabilities)
	}

	state := logic.EntityStateFromSysInfo(info, "")
	if state["light"]["color_temp"] != 4000 {
		t.Fatalf("expected color_temp 4000, got %+v", state["light"])
	}
}

func TestFixtureBulbColor(t *testing.T) {
	info := loadSysInfo(t, "bulb_color.json")

	raw := logic.RawEntitiesFromSysInfo(info, "")
	if len(raw) != 3 {
		t.Fatalf("expected power+brightness+color, got %+v", raw)
	}

	entity := singleEntity(t, info, raw)
	if _, ok := entity.Capabilities["color"]; !ok {
		t.Fatalf("expected color capability, got %+v", entity.Capabilities)
	}

	state := logic.EntityStateFromSysInfo(info, "")
	if state["light"]["hue"] != 120 || state["light"]["saturation"] != 80 {
		t.Fatalf("expected hue/saturation, got %+v", state["light"])
	}
}

func TestFixtureLightStrip(t *testing.T) {
	info := loadSysInfo(t, "light_strip.json")

	raw := logic.RawEntitiesFromSysInfo(info, "")
	if len(raw) != 4 {
		t.Fatalf("expected power+brightness+temp+color, got %+v", raw)
	}

	entity := singleEntity(t, info, raw)
	if _, ok := entity.Capabilities["color_temp"]; !ok {
		t.Fatalf("expected color_temp capability, got %+v", entity.Capabilities)
	}
	if _, ok := entity.Capabilities["color"]; !ok {
		t.Fatalf("expected color capability, got %+v", entity.Capabilities)
	}

	state := logic.EntityStateFromSysInfo(info, "")
	if state["light"]["brightness"] != 40 {
		t.Fatalf("expected brightness 40, got %+v", state["light"])
	}
}

func loadSysInfo(t *testing.T, name string) logic.KasaSysInfo {
	t.Helper()
	path := filepath.Join("fixtures", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var resp logic.KasaResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return resp.System.SysInfo
}

func singleEntity(t *testing.T, info logic.KasaSysInfo, raw []framework.RawEntitySpec) framework.EntitySpec {
	t.Helper()
	entities := logic.EntitiesFromRaw(raw, info, "")
	if len(entities) != 1 || entities[0].ID != "light" {
		t.Fatalf("expected light entity, got %+v", entities)
	}
	return entities[0]
}
