package redfish

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func doPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url,
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func doGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestMockServerPowerCycle(t *testing.T) {
	m := NewMockServer(t)

	if got := m.GetPowerState(); got != PowerOff {
		t.Fatalf("expected PowerOff, got %s", got)
	}

	resp := doPost(t, m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"On"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := m.GetPowerState(); got != PowerOn {
		t.Fatalf("expected PowerOn after reset, got %s", got)
	}

	resets := m.Resets()
	if len(resets) != 1 || resets[0].ResetType != "On" {
		t.Fatalf("unexpected resets: %+v", resets)
	}

	resp = doPost(t, m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"ForceOff"}`)
	resp.Body.Close()
	if got := m.GetPowerState(); got != PowerOff {
		t.Fatalf("expected PowerOff, got %s", got)
	}
}

func TestMockServerVirtualMedia(t *testing.T) {
	m := NewMockServer(t)

	resp := doPost(t,
		m.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		`{"Image":"http://example.com/boot.iso"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	vm := m.GetVirtualMedia()
	if !vm.Inserted || vm.Image != "http://example.com/boot.iso" {
		t.Fatalf("unexpected virtual media state: %+v", vm)
	}

	resp = doGet(t, m.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1")
	defer resp.Body.Close()

	var vmResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&vmResp); err != nil {
		t.Fatal(err)
	}
	if vmResp["Inserted"] != true {
		t.Fatalf("expected Inserted=true, got %v", vmResp["Inserted"])
	}

	resp2 := doPost(t,
		m.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia",
		`{}`)
	resp2.Body.Close()

	vm = m.GetVirtualMedia()
	if vm.Inserted {
		t.Fatal("expected media ejected")
	}
}

func TestMockServerServiceRoot(t *testing.T) {
	m := NewMockServer(t)

	resp := doGet(t, m.URL()+"/redfish/v1")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var root map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	if root["Id"] != "RootService" {
		t.Fatalf("unexpected service root: %+v", root)
	}
}

func TestMockServerGetSystem(t *testing.T) {
	m := NewMockServer(t)

	resp := doGet(t, m.URL()+"/redfish/v1/Systems/1")
	defer resp.Body.Close()

	var sys map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&sys); err != nil {
		t.Fatal(err)
	}
	if sys["PowerState"] != "Off" {
		t.Fatalf("expected Off, got %v", sys["PowerState"])
	}
}

func TestMockServerGetSystems(t *testing.T) {
	m := NewMockServer(t)

	resp := doGet(t, m.URL()+"/redfish/v1/Systems")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var collection map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&collection); err != nil {
		t.Fatal(err)
	}
	if collection["Name"] != "Computer System Collection" {
		t.Fatalf("unexpected collection name: %v", collection["Name"])
	}
	members, ok := collection["Members"].([]any)
	if !ok || len(members) != 1 {
		t.Fatalf("expected 1 member, got %v", collection["Members"])
	}
}

func TestMockServerResetInvalidJSON(t *testing.T) {
	m := NewMockServer(t)

	resp := doPost(t,
		m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{invalid json}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestMockServerVirtualMediaInsertInvalidJSON(t *testing.T) {
	m := NewMockServer(t)

	resp := doPost(t,
		m.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		`not json`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestMockServerGracefulShutdown(t *testing.T) {
	m := NewMockServer(t)

	// Power on first.
	resp := doPost(t, m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"On"}`)
	resp.Body.Close()
	if got := m.GetPowerState(); got != PowerOn {
		t.Fatalf("expected PowerOn, got %s", got)
	}

	// Graceful shutdown.
	resp = doPost(t, m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"GracefulShutdown"}`)
	resp.Body.Close()
	if got := m.GetPowerState(); got != PowerOff {
		t.Fatalf("expected PowerOff after GracefulShutdown, got %s", got)
	}
}

func TestMockServerForceRestart(t *testing.T) {
	m := NewMockServer(t)

	resp := doPost(t, m.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"ForceRestart"}`)
	resp.Body.Close()
	if got := m.GetPowerState(); got != PowerOn {
		t.Fatalf("expected PowerOn after ForceRestart, got %s", got)
	}
}
