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
