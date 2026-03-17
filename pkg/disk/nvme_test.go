//go:build linux

package disk

import (
	"fmt"
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
			name:    "empty label",
			input:   `[{"controller":"/dev/nvme0","namespaces":[{"label":"","sizePct":100}]}]`,
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
	// parseHex was removed in favor of JSON-based namespace listing.
	// This test is retained as a placeholder to document the change.
	t.Skip("parseHex removed: NVMeListNamespaces now uses JSON output")
}

func TestParseNVMeConfig_NegativeSizePct(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":-10}]}]`
	_, err := ParseNVMeConfig(input)
	if err == nil {
		t.Error("expected error for negative sizePct")
	}
}

func TestDetectNVMeControllers(t *testing.T) {
	controllers, err := DetectNVMeControllers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, c := range controllers {
		if !nvmeControllerPathRE.MatchString(c) {
			t.Errorf("DetectNVMeControllers returned non-controller path %q", c)
		}
	}
}

func TestParseNVMeConfig_InvalidControllerPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"namespace path", `[{"controller":"/dev/nvme0n1","namespaces":[{"label":"os","sizePct":100}]}]`},
		{"partition path", `[{"controller":"/dev/nvme0n1p1","namespaces":[{"label":"os","sizePct":100}]}]`},
		{"sata device", `[{"controller":"/dev/sda","namespaces":[{"label":"os","sizePct":100}]}]`},
		{"relative path", `[{"controller":"nvme0","namespaces":[{"label":"os","sizePct":100}]}]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNVMeConfig(tc.input)
			if err == nil {
				t.Error("expected error for invalid controller path")
			}
		})
	}
}

func TestNVMeIdentifyController(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme id-ctrl", []byte(`{"mn":"Samsung SSD 980 PRO","nn":32,"tnvmcap":1000204886016}`), nil)
	mgr := NewManager(cmd)

	info, err := mgr.NVMeIdentifyController(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info["mn"] != "Samsung SSD 980 PRO" {
		t.Errorf("mn = %q, want Samsung SSD 980 PRO", info["mn"])
	}
	if info["nn"] != "32" {
		t.Errorf("nn = %q, want 32", info["nn"])
	}
}

func TestNVMeListNamespaces(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"nsid_list":[{"nsid":1},{"nsid":2},{"nsid":10}]}`), nil)
	mgr := NewManager(cmd)

	nsids, err := mgr.NVMeListNamespaces(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nsids) != 3 {
		t.Fatalf("got %d NSIDs, want 3", len(nsids))
	}
	want := []string{"1", "2", "10"}
	for i, w := range want {
		if nsids[i] != w {
			t.Errorf("nsid[%d] = %q, want %q", i, nsids[i], w)
		}
	}
}

func TestNVMeListNamespacesLegacyArrayOutput(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`[{"nsid":1},{"nsid":2}]`), nil)
	mgr := NewManager(cmd)

	nsids, err := mgr.NVMeListNamespaces(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nsids) != 2 {
		t.Fatalf("got %d NSIDs, want 2", len(nsids))
	}
}

func TestNVMeListNamespaces_InvalidOutput(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"unexpected":true}`), nil)
	mgr := NewManager(cmd)

	_, err := mgr.NVMeListNamespaces(t.Context(), "/dev/nvme0")
	if err == nil {
		t.Fatal("expected parse error for invalid list-ns output")
	}
}

func TestNVMeSupportsMultiNS(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme id-ctrl", []byte(`{"nn":32}`), nil)
	mgr := NewManager(cmd)

	supported, err := mgr.NVMeSupportsMultiNS(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !supported {
		t.Error("expected multi-NS support for nn=32")
	}
}

func TestNVMeSupportsMultiNS_Single(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme id-ctrl", []byte(`{"nn":1}`), nil)
	mgr := NewManager(cmd)

	supported, err := mgr.NVMeSupportsMultiNS(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if supported {
		t.Error("expected no multi-NS support for nn=1")
	}
}

func TestParseNVMeConfig_DuplicateController(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]},{"controller":"/dev/nvme0","namespaces":[{"label":"data","sizePct":100}]}]`
	_, err := ParseNVMeConfig(input)
	if err == nil {
		t.Error("expected error for duplicate controller")
	}
}

func TestNVMeResetNamespaces(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"nsid_list":[{"nsid":1},{"nsid":2}]}`), nil)
	cmd.setResult("nvme delete-ns", []byte(""), nil)
	cmd.setResult("nvme id-ctrl", []byte(`{"tnvmcap":1024000}`), nil)
	cmd.setResult("nvme create-ns", []byte("create-ns: Success, created nsid:1\n"), nil)
	cmd.setResult("nvme attach-ns", []byte(""), nil)
	mgr := NewManager(cmd)

	err := mgr.NVMeResetNamespaces(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNVMeIdentifyController_InvalidPath(t *testing.T) {
	mgr := NewManager(newMockCommander())
	_, err := mgr.NVMeIdentifyController(t.Context(), "/dev/sda")
	if err == nil {
		t.Error("expected error for invalid controller path")
	}
}

func TestNVMeListNamespaces_InvalidPath(t *testing.T) {
	mgr := NewManager(newMockCommander())
	_, err := mgr.NVMeListNamespaces(t.Context(), "/dev/sda")
	if err == nil {
		t.Error("expected error for invalid controller path")
	}
}

func TestParseNVMeConfig_DefaultBlockSize(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]}]`
	configs, err := ParseNVMeConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].Namespaces[0].BlockSize != 512 {
		t.Errorf("blockSize = %d, want 512 (default)", configs[0].Namespaces[0].BlockSize)
	}
}

func TestParseNVMeConfig_EmptyLabel(t *testing.T) {
	input := `[{"controller":"/dev/nvme0","namespaces":[{"label":"","sizePct":100}]}]`
	_, err := ParseNVMeConfig(input)
	if err == nil {
		t.Error("expected error for empty label")
	}
}

func TestApplyNVMeNamespaceLayout(t *testing.T) {
	tests := []struct {
		name    string
		cfgs    []NVMeNamespaceConfig
		mocks   map[string]mockResult
		wantErr bool
	}{
		{
			name: "single controller two namespaces",
			cfgs: []NVMeNamespaceConfig{{
				Controller: "/dev/nvme0",
				Namespaces: []NVMeNamespace{
					{Label: "os", SizePct: 30, BlockSize: 512},
					{Label: "data", SizePct: 70, BlockSize: 4096},
				},
			}},
			mocks: map[string]mockResult{
				"nvme id-ctrl":   {output: []byte(`{"nn":32,"tnvmcap":1024000}`)},
				"nvme list-ns":   {output: []byte(`{"nsid_list":[]}`)},
				"nvme create-ns": {output: []byte("create-ns: Success, created nsid:1\n")},
				"nvme attach-ns": {output: []byte("")},
			},
		},
		{
			name: "controller does not support multi-NS",
			cfgs: []NVMeNamespaceConfig{{
				Controller: "/dev/nvme0",
				Namespaces: []NVMeNamespace{
					{Label: "os", SizePct: 100, BlockSize: 512},
				},
			}},
			mocks: map[string]mockResult{
				"nvme id-ctrl": {output: []byte(`{"nn":1}`)},
			},
			wantErr: true,
		},
		{
			name: "create-ns fails",
			cfgs: []NVMeNamespaceConfig{{
				Controller: "/dev/nvme0",
				Namespaces: []NVMeNamespace{
					{Label: "os", SizePct: 100, BlockSize: 512},
				},
			}},
			mocks: map[string]mockResult{
				"nvme id-ctrl":   {output: []byte(`{"nn":32,"tnvmcap":1024000}`)},
				"nvme list-ns":   {output: []byte(`{"nsid_list":[]}`)},
				"nvme create-ns": {err: fmt.Errorf("device busy")},
			},
			wantErr: true,
		},
		{
			name: "delete existing namespaces then create",
			cfgs: []NVMeNamespaceConfig{{
				Controller: "/dev/nvme0",
				Namespaces: []NVMeNamespace{
					{Label: "os", SizePct: 100, BlockSize: 512},
				},
			}},
			mocks: map[string]mockResult{
				"nvme id-ctrl":   {output: []byte(`{"nn":32,"tnvmcap":1024000}`)},
				"nvme list-ns":   {output: []byte(`{"nsid_list":[{"nsid":1}]}`)},
				"nvme delete-ns": {output: []byte("")},
				"nvme create-ns": {output: []byte("create-ns: Success, created nsid:2\n")},
				"nvme attach-ns": {output: []byte("")},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newMockCommander()
			for k, v := range tc.mocks {
				cmd.setResult(k, v.output, v.err)
			}
			mgr := NewManager(cmd)

			created, err := mgr.ApplyNVMeNamespaceLayout(t.Context(), tc.cfgs)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(created) != len(tc.cfgs) {
				t.Fatalf("got %d controller results, want %d", len(created), len(tc.cfgs))
			}
		})
	}
}

func TestNVMeResetNamespaces_DeleteFails(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"nsid_list":[{"nsid":1}]}`), nil)
	cmd.setResult("nvme delete-ns", nil, fmt.Errorf("delete failed"))
	mgr := NewManager(cmd)

	err := mgr.NVMeResetNamespaces(t.Context(), "/dev/nvme0")
	if err == nil {
		t.Fatal("expected error when delete-ns fails")
	}
}

func TestNVMeResetNamespaces_CreateFails(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"nsid_list":[]}`), nil)
	cmd.setResult("nvme id-ctrl", []byte(`{"tnvmcap":1024000}`), nil)
	cmd.setResult("nvme create-ns", nil, fmt.Errorf("create failed"))
	mgr := NewManager(cmd)

	err := mgr.NVMeResetNamespaces(t.Context(), "/dev/nvme0")
	if err == nil {
		t.Fatal("expected error when create-ns fails")
	}
}

func TestNVMeListNamespaces_EmptyOutput(t *testing.T) {
	cmd := newMockCommander()
	cmd.setResult("nvme list-ns", []byte(`{"nsid_list":[]}`), nil)
	mgr := NewManager(cmd)

	nsids, err := mgr.NVMeListNamespaces(t.Context(), "/dev/nvme0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nsids) != 0 {
		t.Errorf("expected 0 NSIDs, got %d", len(nsids))
	}
}
