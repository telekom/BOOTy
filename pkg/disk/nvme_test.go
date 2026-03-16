//go:build linux

package disk

import (
	"testing"
)

func TestParseNVMeConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		count   int
	}{
		{
			name:    "valid single controller",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":50},{"label":"data","sizePct":50,"blockSize":4096}]}]`,
			wantErr: false,
			count:   1,
		},
		{
			name:    "valid multiple controllers",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]},{"controller":"/dev/nvme1","namespaces":[{"label":"data","sizePct":100}]}]`,
			wantErr: false,
			count:   2,
		},
		{
			name:    "invalid json",
			input:   `{bad}`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configs, err := ParseNVMeConfig(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(configs) != tc.count {
				t.Errorf("got %d configs, want %d", len(configs), tc.count)
			}
		})
	}
}

func TestParseNVMeConfigNamespaceFields(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":60,"blockSize":4096}]}]`
	configs, err := ParseNVMeConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	ns := configs[0].Namespaces[0]
	if ns.Label != "os" {
		t.Errorf("label = %q, want os", ns.Label)
	}
	if ns.SizePct != 60 {
		t.Errorf("sizePct = %d, want 60", ns.SizePct)
	}
	if ns.BlockSize != 4096 {
		t.Errorf("blockSize = %d, want 4096", ns.BlockSize)
	}
}

func TestNVMeControllerRegex(t *testing.T) {
	tests := []struct {
		name  string
		match bool
	}{
		{"nvme0", true},
		{"nvme1", true},
		{"nvme10", true},
		{"nvme0n1", false},
		{"nvme0n1p1", false},
		{"sda", false},
		{"nvme", false},
	}
	for _, tc := range tests {
		if got := nvmeControllerRE.MatchString(tc.name); got != tc.match {
			t.Errorf("nvmeControllerRE.MatchString(%q) = %v, want %v", tc.name, got, tc.match)
		}
	}
}

func TestParseNVMeConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "empty controller",
			input:   `[{"controller":"","namespaces":[{"label":"os","sizePct":100}]}]`,
			wantErr: true,
		},
		{
			name:    "empty namespaces",
			input:   `[{"controller":"/dev/nvme0","namespaces":[]}]`,
			wantErr: true,
		},
		{
			name:    "sizePct zero",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":0}]}]`,
			wantErr: true,
		},
		{
			name:    "sizePct over 100",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":101}]}]`,
			wantErr: true,
		},
		{
			name:    "total sizePct exceeds 100",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":60},{"label":"data","sizePct":50}]}]`,
			wantErr: true,
		},
		{
			name:    "invalid blockSize",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100,"blockSize":1024}]}]`,
			wantErr: true,
		},
		{
			name:    "valid config",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":50},{"label":"data","sizePct":50,"blockSize":4096}]}]`,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNVMeConfig(tc.input)
			if tc.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		input   string
		want    uint64
		wantErr bool
	}{
		{"1", 1, false},
		{"a", 10, false},
		{"ff", 255, false},
		{"0", 0, false},
		{"", 0, true},
		{"xyz", 0, true},
	}
	for _, tc := range tests {
		got, err := parseHex(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseHex(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHex(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseHex(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseNVMeConfig_NegativeSizePct(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":-10}]}]`
	_, err := ParseNVMeConfig(input)
	if err == nil {
		t.Error("expected error for negative sizePct")
	}
}

func TestDetectNVMeControllers(t *testing.T) {
	// Just exercises the code path - results depend on host hardware.
	controllers := DetectNVMeControllers()
	// On most CI/dev machines there are no NVMe controllers.
	_ = controllers
}
