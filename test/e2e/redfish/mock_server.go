// Package redfish provides a mock Redfish BMC server for E2E testing.
// It serves static Redfish JSON responses without requiring libvirt or sushy-tools.
package redfish

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// PowerState represents a Redfish system power state.
type PowerState string

const (
	// PowerOn represents the "On" power state.
	PowerOn PowerState = "On"
	// PowerOff represents the "Off" power state.
	PowerOff PowerState = "Off"
)

// ResetAction tracks a reset request.
type ResetAction struct {
	ResetType string `json:"ResetType"`
}

// VirtualMediaAction tracks a virtual media insert/eject.
type VirtualMediaAction struct {
	Image    string `json:"Image,omitempty"`
	Inserted bool   `json:"Inserted"`
}

// MockServer wraps httptest.Server with Redfish BMC state.
type MockServer struct {
	Server *httptest.Server
	T      testing.TB

	mu           sync.Mutex
	powerState   PowerState
	resets       []ResetAction
	virtualMedia VirtualMediaAction
}

// NewMockServer creates a Redfish mock server for testing.
func NewMockServer(tb testing.TB) *MockServer { //nolint:thelper // not a test helper
	tb.Helper()
	m := &MockServer{
		T:          tb,
		powerState: PowerOff,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /redfish/v1", m.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/", m.handleServiceRoot)
	mux.HandleFunc("GET /redfish/v1/Systems", m.handleSystems)
	mux.HandleFunc("GET /redfish/v1/Systems/1", m.handleSystem)
	mux.HandleFunc("POST /redfish/v1/Systems/1/Actions/ComputerSystem.Reset", m.handleReset)
	mux.HandleFunc("GET /redfish/v1/Managers/1/VirtualMedia/CD1", m.handleVirtualMediaGet)
	mux.HandleFunc("POST /redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia", m.handleVirtualMediaInsert)
	mux.HandleFunc("POST /redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia", m.handleVirtualMediaEject)

	m.Server = httptest.NewServer(mux)
	tb.Cleanup(m.Server.Close)
	return m
}

// URL returns the server's base URL.
func (m *MockServer) URL() string {
	return m.Server.URL
}

// GetPowerState returns the current power state.
func (m *MockServer) GetPowerState() PowerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.powerState
}

// Resets returns all reset actions received.
func (m *MockServer) Resets() []ResetAction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ResetAction{}, m.resets...)
}

// GetVirtualMedia returns current virtual media state.
func (m *MockServer) GetVirtualMedia() VirtualMediaAction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.virtualMedia
}

func (m *MockServer) handleServiceRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"@odata.type": "#ServiceRoot.v1_9_0.ServiceRoot",
		"@odata.id":   "/redfish/v1",
		"Id":          "RootService",
		"Name":        "Root Service",
		"Systems":     map[string]string{"@odata.id": "/redfish/v1/Systems"},
		"Managers":    map[string]string{"@odata.id": "/redfish/v1/Managers"},
	})
}

func (m *MockServer) handleSystems(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"@odata.type":         "#ComputerSystemCollection.ComputerSystemCollection",
		"@odata.id":           "/redfish/v1/Systems",
		"Name":                "Computer System Collection",
		"Members@odata.count": 1,
		"Members": []map[string]string{
			{"@odata.id": "/redfish/v1/Systems/1"},
		},
	})
}

func (m *MockServer) handleSystem(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	state := m.powerState
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"@odata.type": "#ComputerSystem.v1_13_0.ComputerSystem",
		"@odata.id":   "/redfish/v1/Systems/1",
		"Id":          "1",
		"Name":        "System",
		"PowerState":  string(state),
		"Boot": map[string]any{
			"BootSourceOverrideEnabled": "Once",
			"BootSourceOverrideTarget":  "Cd",
		},
		"Actions": map[string]any{
			"#ComputerSystem.Reset": map[string]any{
				"target":                            "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
				"ResetType@Redfish.AllowableValues": []string{"On", "ForceOff", "ForceRestart", "GracefulShutdown"},
			},
		},
	})
}

func (m *MockServer) handleReset(w http.ResponseWriter, r *http.Request) {
	var action ResetAction
	if err := json.NewDecoder(r.Body).Decode(&action); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.resets = append(m.resets, action)
	switch action.ResetType {
	case "On", "ForceRestart":
		m.powerState = PowerOn
	case "ForceOff", "GracefulShutdown":
		m.powerState = PowerOff
	}
	m.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (m *MockServer) handleVirtualMediaGet(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	vm := m.virtualMedia
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"@odata.type":  "#VirtualMedia.v1_3_0.VirtualMedia",
		"@odata.id":    "/redfish/v1/Managers/1/VirtualMedia/CD1",
		"Id":           "CD1",
		"Name":         "Virtual CD",
		"MediaTypes":   []string{"CD", "DVD"},
		"Image":        vm.Image,
		"Inserted":     vm.Inserted,
		"ConnectedVia": "URI",
	})
}

func (m *MockServer) handleVirtualMediaInsert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"Image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.virtualMedia = VirtualMediaAction{Image: req.Image, Inserted: true}
	m.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (m *MockServer) handleVirtualMediaEject(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	m.virtualMedia = VirtualMediaAction{}
	m.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
