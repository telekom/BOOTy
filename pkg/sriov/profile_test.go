package sriov

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func ptr[T any](v T) *T { return &v }

// fullProfile sets every field with a valid value.
func fullProfile() Profile {
	return Profile{
		Name:  "cx6dx-2p",
		Match: Match{VendorID: "15b3", DeviceID: "101d", Driver: "mlx5_core", MAC: "B8:CE:F6:00:00:01", Interface: "ens1f*"},
		Firmware: FirmwareSettings{
			SRIOVEnabled: ptr(true), MaxVFs: ptr(64), LinkType: "eth", FWLLDPAgent: ptr(false),
			NVConfig:      []Setting{{"CQE_COMPRESSION", "1"}, {"FLEX_PARSER_PROFILE_ENABLE", "4"}},
			DevlinkParams: []Setting{{"enable_sriov", "true"}},
		},
		Runtime: RuntimeSettings{
			NumVFs: ptr(8), EswitchMode: "switchdev", Handoff: "reset",
			VFDefaults: VFSettings{Trust: ptr(false), SpoofChk: ptr(true), LinkState: "auto"},
			VFs: []VFOverride{{Index: 0, MAC: "02:00:00:00:00:10",
				VFSettings: VFSettings{VLAN: ptr(100), QoS: ptr(3), MinTxRate: ptr(100), MaxTxRate: ptr(1000)}}},
		},
		OnDrift: "fail",
	}
}

func TestValidateAcceptsFullConfig(t *testing.T) {
	c := Config{Profiles: []Profile{fullProfile()}, OnDrift: "apply", NICTools: "vendor"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&Config{}).Validate(); err != nil {
		t.Fatalf("empty config: %v", err)
	}
}

// TestValidateRejects changes one field of a valid profile per case; every
// case must fail with a message that names the field.
func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *Config)
		want   string
	}{
		{"onDrift", func(c *Config) { c.OnDrift = "Apply" }, "SRIOV_ON_DRIFT"},
		{"nicTools", func(c *Config) { c.NICTools = "mft" }, "NIC_TOOLS"},
		{"duplicate", func(c *Config) { c.Profiles = append(c.Profiles, c.Profiles[0]) }, "defined twice"},
		{"name", func(c *Config) { c.Profiles[0].Name = "" }, "name must be"},
		{"name chars", func(c *Config) { c.Profiles[0].Name = "a b" }, "name must be"},
		{"profile onDrift", func(c *Config) { c.Profiles[0].OnDrift = "ignore" }, "onDrift"},
		{"empty match", func(c *Config) { c.Profiles[0].Match = Match{} }, "match needs"},
		{"vendor", func(c *Config) { c.Profiles[0].Match.VendorID = "0x15b3" }, "match.vendorId"},
		{"vendor case", func(c *Config) { c.Profiles[0].Match.VendorID = "15B3" }, "match.vendorId"},
		{"device", func(c *Config) { c.Profiles[0].Match.DeviceID = "101" }, "match.deviceId"},
		{"driver", func(c *Config) { c.Profiles[0].Match.Driver = "mlx5 core" }, "match.driver"},
		{"mac", func(c *Config) { c.Profiles[0].Match.MAC = "b8:ce:f6" }, "match.mac"},
		{"mac 64", func(c *Config) { c.Profiles[0].Match.MAC = "00:00:00:00:fe:80:00:00" }, "match.mac"},
		{"interface", func(c *Config) { c.Profiles[0].Match.Interface = "ens[" }, "match.interface"},
		{"interface slash", func(c *Config) { c.Profiles[0].Match.Interface = "a/b" }, "match.interface"},
		{"maxVFs", func(c *Config) { c.Profiles[0].Firmware.MaxVFs = ptr(-1) }, "firmware.maxVFs"},
		{"maxVFs high", func(c *Config) { c.Profiles[0].Firmware.MaxVFs = ptr(65536) }, "firmware.maxVFs"},
		{"maxVFs without sriov", func(c *Config) {
			c.Profiles[0].Firmware.SRIOVEnabled = ptr(false)
			c.Profiles[0].Runtime = RuntimeSettings{}
		}, "needs firmware.sriovEnabled"},
		{"numVFs without sriov", func(c *Config) {
			c.Profiles[0].Firmware.SRIOVEnabled = ptr(false)
			c.Profiles[0].Firmware.MaxVFs = nil
		}, "runtime.numVFs needs firmware.sriovEnabled"},
		{"linkType", func(c *Config) { c.Profiles[0].Firmware.LinkType = "ethernet" }, "firmware.linkType"},
		{"nv name", func(c *Config) { c.Profiles[0].Firmware.NVConfig[0].Name = "cqe" }, "firmware.nvConfig name"},
		{"nv value", func(c *Config) { c.Profiles[0].Firmware.NVConfig[0].Value = "1 2" }, "value"},
		{"nv empty value", func(c *Config) { c.Profiles[0].Firmware.NVConfig[0].Value = "" }, "value"},
		{"nv twice", func(c *Config) {
			c.Profiles[0].Firmware.NVConfig = append(c.Profiles[0].Firmware.NVConfig, Setting{"CQE_COMPRESSION", "0"})
		}, "set twice"},
		{"nv named", func(c *Config) { c.Profiles[0].Firmware.NVConfig[0].Name = "NUM_OF_VFS" }, "use sriovEnabled"},
		{"nv link type", func(c *Config) { c.Profiles[0].Firmware.NVConfig[0].Name = "LINK_TYPE_P2" }, "use sriovEnabled"},
		{"param name", func(c *Config) { c.Profiles[0].Firmware.DevlinkParams[0].Name = "Enable" }, "devlinkParams name"},
		{"numVFs", func(c *Config) { c.Profiles[0].Runtime.NumVFs = ptr(-2) }, "runtime.numVFs"},
		{"numVFs above max", func(c *Config) { c.Profiles[0].Runtime.NumVFs = ptr(65) }, "above firmware.maxVFs"},
		{"eswitch", func(c *Config) { c.Profiles[0].Runtime.EswitchMode = "Switchdev" }, "runtime.eswitchMode"},
		{"handoff", func(c *Config) { c.Profiles[0].Runtime.Handoff = "leave" }, "runtime.handoff"},
		{"vf without numVFs", func(c *Config) { c.Profiles[0].Runtime.NumVFs = ptr(0) }, "need runtime.numVFs"},
		{"vf index", func(c *Config) { c.Profiles[0].Runtime.VFs[0].Index = 8 }, "out of the VF range"},
		{"vf negative", func(c *Config) { c.Profiles[0].Runtime.VFs[0].Index = -1 }, "out of the VF range"},
		{"vf twice", func(c *Config) {
			c.Profiles[0].Runtime.VFs = append(c.Profiles[0].Runtime.VFs, VFOverride{Index: 0})
		}, "set twice"},
		{"vf mac", func(c *Config) { c.Profiles[0].Runtime.VFs[0].MAC = "zz" }, "unicast"},
		{"vf multicast", func(c *Config) { c.Profiles[0].Runtime.VFs[0].MAC = "01:00:5e:00:00:01" }, "unicast"},
		{"vlan", func(c *Config) { c.Profiles[0].Runtime.VFs[0].VLAN = ptr(4096) }, ".vlan"},
		{"qos", func(c *Config) { c.Profiles[0].Runtime.VFs[0].QoS = ptr(8) }, ".qos"},
		{"qos without vlan", func(c *Config) { c.Profiles[0].Runtime.VFs[0].VLAN = ptr(0) }, "qos needs a vlan"},
		{"min rate", func(c *Config) { c.Profiles[0].Runtime.VFs[0].MinTxRate = ptr(-1) }, "minTxRate"},
		{"max rate", func(c *Config) { c.Profiles[0].Runtime.VFDefaults.MaxTxRate = ptr(-1) }, "maxTxRate"},
		{"min above max", func(c *Config) { c.Profiles[0].Runtime.VFs[0].MinTxRate = ptr(2000) }, "minTxRate is above"},
		{"link state", func(c *Config) { c.Profiles[0].Runtime.VFDefaults.LinkState = "up" }, "linkState"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Profiles: []Profile{fullProfile()}}
			tc.mutate(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error with %q", err, tc.want)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	c := &Config{}
	p := &Profile{}
	if c.DriftAction(p) != OnDriftWarn || c.Tools() != ToolsOpenSource {
		t.Fatalf("defaults: %s %s", c.DriftAction(p), c.Tools())
	}
	c.OnDrift = OnDriftFail
	if c.DriftAction(p) != OnDriftFail {
		t.Fatal("config onDrift ignored")
	}
	p.OnDrift = OnDriftApply
	if c.DriftAction(p) != OnDriftApply {
		t.Fatal("profile onDrift does not win")
	}
	c.NICTools = ToolsKernel
	if c.Tools() != ToolsKernel {
		t.Fatal("nicTools ignored")
	}
}

func TestMatches(t *testing.T) {
	pf := &PF{Interface: "ens1f0np0", VendorID: "15b3", DeviceID: "101d", Driver: "mlx5_core", MAC: "b8:ce:f6:00:00:01"}
	for _, m := range []Match{
		{VendorID: "15b3"}, {DeviceID: "101d"}, {Driver: "mlx5_core"}, {MAC: "B8-CE-F6-00-00-01"},
		{Interface: "ens1f*"}, {Interface: "ens1f0np0"},
		{VendorID: "15b3", DeviceID: "101d", Driver: "mlx5_core", MAC: "b8:ce:f6:00:00:01", Interface: "ens?f0np0"},
	} {
		if !m.Matches(pf) {
			t.Errorf("%+v does not match", m)
		}
	}
	for _, m := range []Match{
		{VendorID: "8086"}, {DeviceID: "1017"}, {Driver: "ice"}, {MAC: "b8:ce:f6:00:00:02"},
		{Interface: "ens2*"}, {Interface: "ens1f0"}, {Interface: "["},
		{VendorID: "15b3", Driver: "ice"},
	} {
		if m.Matches(pf) {
			t.Errorf("%+v matches", m)
		}
	}
}

// TestDecodeJSONAndYAML decodes the same profile in both formats; the VF
// override fields sit next to index in both.
func TestDecodeJSONAndYAML(t *testing.T) {
	js := `{"name":"e810","match":{"driver":"ice"},"firmware":{"fwLLDPAgent":false},
	 "runtime":{"numVFs":4,"vfDefaults":{"spoofChk":true},"vfs":[{"index":1,"mac":"02:00:00:00:00:01","vlan":10}]}}`
	ym := `
name: e810
match: {driver: ice}
firmware: {fwLLDPAgent: false}
runtime:
  numVFs: 4
  vfDefaults: {spoofChk: true}
  vfs:
    - {index: 1, mac: "02:00:00:00:00:01", vlan: 10}
`
	var fromJSON, fromYAML Profile
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fromJSON); err != nil {
		t.Fatal(err)
	}
	yd := yaml.NewDecoder(strings.NewReader(ym))
	yd.KnownFields(true)
	if err := yd.Decode(&fromYAML); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]Profile{"json": fromJSON, "yaml": fromYAML} {
		if p.Runtime.VFs[0].VLAN == nil || *p.Runtime.VFs[0].VLAN != 10 || p.Runtime.VFs[0].MAC != "02:00:00:00:00:01" ||
			p.Firmware.FWLLDPAgent == nil || *p.Firmware.FWLLDPAgent || *p.Runtime.VFDefaults.SpoofChk != true {
			t.Errorf("%s: %+v", name, p)
		}
		c := Config{Profiles: []Profile{p}}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
