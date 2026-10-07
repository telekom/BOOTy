//go:build e2e

// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package redfish

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRedfishProtocolCharacterization(t *testing.T) {
	server := NewMockServer(t)
	response := doGet(t, server.URL()+"/redfish/v1/")
	defer response.Body.Close()
	var root struct {
		Systems struct {
			ID string `json:"@odata.id"`
		}
	}
	if err := json.NewDecoder(response.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	if root.Systems.ID != "/redfish/v1/Systems" {
		t.Fatalf("systems discovery link = %q", root.Systems.ID)
	}
	for _, reset := range []string{"On", "ForceRestart", "GracefulShutdown", "ForceOff"} {
		response := doPost(t, server.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
			`{"ResetType":"`+reset+`"}`)
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("%s reset status = %d", reset, response.StatusCode)
		}
	}
	resets := server.Resets()
	if len(resets) != 4 {
		t.Fatalf("reset history = %+v", resets)
	}
	resets[0].ResetType = "mutated"
	if server.Resets()[0].ResetType != "On" {
		t.Fatal("reset snapshot aliases live state")
	}
	if server.GetPowerState() != PowerOff {
		t.Fatal("power cycle did not finish off")
	}
}
