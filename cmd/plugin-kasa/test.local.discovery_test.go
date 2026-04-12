//go:build integration

package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	app "github.com/slidebolt/plugin-kasa/app"
	translate "github.com/slidebolt/plugin-kasa/internal/translate"
	domain "github.com/slidebolt/sb-domain"
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

	env := testkit.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	t.Setenv("KASA_SUBNET", subnet)
	p := app.New()
	deps := map[string]json.RawMessage{
		"messenger": env.MessengerPayload(),
	}
	if _, err := p.OnStart(deps); err != nil {
		t.Fatalf("plugin OnStart: %v", err)
	}
	t.Cleanup(func() { _ = p.OnShutdown() })

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
