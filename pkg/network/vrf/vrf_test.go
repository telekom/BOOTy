package vrf

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		err  bool
	}{
		{
			"valid",
			Config{Name: "mgmt", TableID: 100, Members: []string{"eth0"}},
			false,
		},
		{
			"empty name",
			Config{TableID: 100},
			true,
		},
		{
			"zero table",
			Config{Name: "mgmt", TableID: 0},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.err {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

func TestMultiVRFConfig_AllConfigs(t *testing.T) {
	m := &MultiVRFConfig{
		Enabled:      true,
		Management:   &Config{Name: "mgmt", TableID: 100, Members: []string{"eth0"}},
		Provisioning: &Config{Name: "prov", TableID: 200, Members: []string{"vxlan100"}},
	}
	all := m.AllConfigs()
	if len(all) != 2 {
		t.Fatalf("AllConfigs() = %d, want 2", len(all))
	}
	if all[0].Name != "mgmt" {
		t.Errorf("first = %q, want mgmt", all[0].Name)
	}
}

func TestMultiVRFConfig_Disabled(t *testing.T) {
	m := &MultiVRFConfig{
		Enabled:    false,
		Management: &Config{Name: "mgmt", TableID: 100},
	}
	all := m.AllConfigs()
	if len(all) != 0 {
		t.Errorf("AllConfigs() = %d, want 0 (disabled)", len(all))
	}
}

func TestMultiVRFConfig_Nil(t *testing.T) {
	var m *MultiVRFConfig
	all := m.AllConfigs()
	if len(all) != 0 {
		t.Errorf("AllConfigs() on nil = %d", len(all))
	}
}

func TestMultiVRFConfig_ValidateAll(t *testing.T) {
	tests := []struct {
		name string
		cfg  MultiVRFConfig
		err  bool
	}{
		{
			"valid",
			MultiVRFConfig{
				Enabled:      true,
				Management:   &Config{Name: "mgmt", TableID: 100},
				Provisioning: &Config{Name: "prov", TableID: 200},
			},
			false,
		},
		{
			"duplicate name",
			MultiVRFConfig{
				Enabled:      true,
				Management:   &Config{Name: "mgmt", TableID: 100},
				Provisioning: &Config{Name: "mgmt", TableID: 200},
			},
			true,
		},
		{
			"duplicate table",
			MultiVRFConfig{
				Enabled:      true,
				Management:   &Config{Name: "mgmt", TableID: 100},
				Provisioning: &Config{Name: "prov", TableID: 100},
			},
			true,
		},
		{
			"invalid child",
			MultiVRFConfig{
				Enabled:    true,
				Management: &Config{Name: "", TableID: 100},
			},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateAll()
			if (err != nil) != tc.err {
				t.Errorf("ValidateAll() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

func TestMultiVRFConfig_WithExtra(t *testing.T) {
	m := &MultiVRFConfig{
		Enabled: true,
		Extra: []Config{
			{Name: "extra1", TableID: 300},
			{Name: "extra2", TableID: 400},
		},
	}
	all := m.AllConfigs()
	if len(all) != 2 {
		t.Fatalf("AllConfigs() = %d, want 2", len(all))
	}
}
