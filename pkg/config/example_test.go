package config_test

import (
	"fmt"

	"github.com/telekom/BOOTy/pkg/config"
)

// ExampleLoad demonstrates loading a YAML configuration file.
// Format is detected from the file extension (.yaml, .yml, .json).
func ExampleLoad() {
	cfg, err := config.Load("testdata/full.yaml")
	if err != nil {
		panic(err)
	}
	fmt.Println(cfg.Hostname)
	fmt.Println(cfg.Mode)
	fmt.Println(cfg.Network.BGP.ASN)
	// Output:
	// node-01
	// provision
	// 65001
}

// ExampleConfig_Validate demonstrates constructing a Config programmatically
// and validating it — the typical pattern for an external controller building
// a config before submitting it to BOOTy.
func ExampleConfig_Validate() {
	cfg := &config.Config{
		Hostname: "spine-01",
		Mode:     "provision",
		Network: config.NetworkConfig{
			Mode: "gobgp",
			BGP:  config.BGPConfig{ASN: 65001, PeerMode: "unnumbered"},
		},
		Transport: config.TransportConfig{
			Token:      "bootstrap-token",
			InitURL:    "https://caprf.example.com/status/init",
			SuccessURL: "https://caprf.example.com/status/success",
		},
		Provision: config.ProvisionConfig{
			Image: config.ImageConfig{
				URLs:         []string{"https://registry.example.com/image:v1"},
				Checksum:     "abc123def456abc123def456abc123def456abc123def456abc123def456ab12",
				ChecksumType: "sha256",
				Mode:         "whole-disk",
			},
			Disk: config.DiskConfig{MinSizeGB: 100},
		},
	}

	if err := cfg.Validate(); err != nil {
		fmt.Println("invalid:", err)
		return
	}
	// After Validate(), enum fields are normalized to their canonical form.
	fmt.Println(cfg.Network.BGP.PeerMode) // already lowercase
	fmt.Println(cfg.Provision.Image.ChecksumType)
	// Output:
	// unnumbered
	// sha256
}

// ExampleLoadWithOptions demonstrates strict mode, which rejects unknown
// YAML/JSON keys — useful for catching configuration typos in CI.
func ExampleLoadWithOptions() {
	_, err := config.LoadWithOptions(config.LoadOptions{
		Path:   "testdata/full.yaml",
		Strict: false, // set to true to reject unknown keys
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("loaded ok")
	// Output:
	// loaded ok
}

// ExampleParsePartitionLayout demonstrates building a declarative partition
// layout from JSON — used by external controllers to configure disk layout
// before embedding it in a ProvisionConfig.
func ExampleParsePartitionLayout() {
	const layoutJSON = `{
		"table": "gpt",
		"partitions": [
			{"label": "efi",  "sizeMB": 512,  "filesystem": "vfat", "mountpoint": "/boot/efi"},
			{"label": "boot", "sizeMB": 1024, "filesystem": "ext4", "mountpoint": "/boot"},
			{"label": "root", "sizeMB": 0,    "filesystem": "ext4", "mountpoint": "/"}
		]
	}`

	layout, err := config.ParsePartitionLayout(layoutJSON)
	if err != nil {
		fmt.Println("invalid:", err)
		return
	}
	fmt.Println(layout.Table)
	fmt.Println(len(layout.Partitions))
	fmt.Println(layout.Partitions[2].Mountpoint)
	// Output:
	// gpt
	// 3
	// /
}
