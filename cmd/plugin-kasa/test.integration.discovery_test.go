//go:build integration

package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	translate "github.com/slidebolt/plugin-kasa/internal/translate"
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
