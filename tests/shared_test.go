package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lms-io/module-framework/pkg/framework"
	 "github.com/slidebolt/plugin-kasa/pkg/logic"
)

// --- Mock KasaClient ---

type MockKasaClient struct {
	Callback        func(ip string, info logic.KasaSysInfo)
	OnSetPower      func(state int)
	OnSetLightState func(params map[string]any)
}

func (m *MockKasaClient) SendUDPProbe() error { return nil }
func (m *MockKasaClient) ListenUDP(cb func(ip string, info logic.KasaSysInfo)) (func(), error) {
	m.Callback = cb
	return func() {}, nil
}
func (m *MockKasaClient) SetPower(ip, childID string, state int) error {
	if m.OnSetPower != nil {
		m.OnSetPower(state)
	}
	return nil
}
func (m *MockKasaClient) SetLightState(ip string, params map[string]any) error {
	if m.OnSetLightState != nil {
		m.OnSetLightState(params)
	}
	return nil
}
func (m *MockKasaClient) GetSysInfo(ip string) (*logic.KasaSysInfo, error) { return nil, nil }
func (m *MockKasaClient) Close() error                                     { return nil }
func (m *MockKasaClient) TriggerDiscovery(ip string, info logic.KasaSysInfo) {
	if m.Callback != nil {
		m.Callback(ip, info)
	}
}

// --- Mock ModuleAPI ---

type testAPI struct {
	mu        sync.Mutex
	id        string
	config    map[string]any
	instances []framework.InstanceConfig
	states    map[string]map[string]map[string]any
	subs      map[string][]chan framework.Event
	ctx       context.Context
}

func newTestAPI(id string, config map[string]any) *testAPI {
	return &testAPI{
		id:     id,
		config: config,
		states: make(map[string]map[string]map[string]any),
		subs:   make(map[string][]chan framework.Event),
		ctx:    context.Background(),
	}
}

func (a *testAPI) ModuleID() string                         { return a.id }
func (a *testAPI) Context() context.Context                 { return a.ctx }
func (a *testAPI) GetModuleConfig() map[string]any          { return a.config }
func (a *testAPI) SetBundleStatus(s framework.BundleStatus) {}

func (a *testAPI) RegisterInstance(cfg framework.InstanceConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, inst := range a.instances {
		if inst.ID == cfg.ID {
			a.instances[i] = cfg
			return nil
		}
	}
	a.instances = append(a.instances, cfg)
	return nil
}

func (a *testAPI) DeleteInstance(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.instances[:0]
	for _, inst := range a.instances {
		if inst.ID != id {
			out = append(out, inst)
		}
	}
	a.instances = out
	delete(a.states, id)
	return nil
}

func (a *testAPI) UpdateEntityState(id string, state map[string]map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.states[id] == nil {
		a.states[id] = make(map[string]map[string]any)
	}
	for eid, s := range state {
		a.states[id][eid] = s
	}
	return nil
}

func (a *testAPI) GetInstances() []framework.InstanceConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]framework.InstanceConfig, len(a.instances))
	copy(cp, a.instances)
	return cp
}

func (a *testAPI) Publish(topic, eventType string, data map[string]any) {
	a.mu.Lock()
	chs := a.subs[topic]
	a.mu.Unlock()
	for _, ch := range chs {
		select {
		case ch <- framework.Event{Topic: topic, Type: eventType, Data: data}:
		default:
		}
	}
}

func (a *testAPI) Listen(topic string) <-chan framework.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := make(chan framework.Event, 16)
	a.subs[topic] = append(a.subs[topic], ch)
	return ch
}

func (a *testAPI) Subscribe(deviceID string, entityID ...string) <-chan framework.Event {
	if deviceID == "" {
		return a.Listen("")
	}
	if len(entityID) > 0 && entityID[0] != "" {
		return a.Listen("state/" + deviceID + "/" + entityID[0])
	}
	return a.Listen("state/" + deviceID)
}

func (a *testAPI) Unsubscribe(topic string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.subs, topic)
}

func (a *testAPI) Send(topic, eventType string, data map[string]any) {
	a.Publish(topic, eventType, data)
}

func (a *testAPI) Info(msg string, args ...any)  {}
func (a *testAPI) Warn(msg string, args ...any)  {}
func (a *testAPI) Error(msg string, args ...any) {}
func (a *testAPI) Debug(msg string, args ...any) {}

// --- Helpers ---

func waitForDevice(t *testing.T, api *testAPI, id string) framework.InstanceConfig {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("Timeout waiting for device %s", id)
		case <-time.After(100 * time.Millisecond):
			api.mu.Lock()
			for _, inst := range api.instances {
				if inst.ID == id {
					api.mu.Unlock()
					return inst
				}
			}
			api.mu.Unlock()
		}
	}
}
