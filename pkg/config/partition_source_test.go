package config

import (
	"strings"
	"testing"
)

func TestValidatePartitionLayoutSources(t *testing.T) {
	yes := true
	for _, tc := range []struct {
		name      string
		url       string
		path      string
		mode      string
		allowHTTP bool
		inline    bool
		wantErr   string
	}{
		{name: "none"},
		{name: "https URL", url: " https://layouts.example.invalid/cis.json "},
		{name: "http URL needs opt-in", url: "http://layouts.example.invalid/cis.json", wantErr: "plain HTTP"},
		{name: "http URL with opt-in", url: "http://layouts.example.invalid/cis.json", allowHTTP: true},
		{name: "relative URL", url: "/cis.json", wantErr: "absolute HTTP(S) URL"},
		{name: "file URL", url: "file://host/cis.json", wantErr: "not supported"},
		{name: "image path pattern", path: "/usr/share/vendor/layout/*.json"},
		{name: "relative image path", path: "usr/share/layout.json", wantErr: "absolute, clean path"},
		{name: "unclean image path", path: "/usr/share/../layout.json", wantErr: "absolute, clean path"},
		{name: "bad pattern", path: "/usr/share/[.json", wantErr: "syntax error"},
		{name: "A/B mode", path: "/usr/share/layout.json", mode: ImageModeAB, wantErr: "need image mode whole-disk"},
		{name: "partition mode", url: "https://layouts.example.invalid/cis.json", mode: ImageModePartition, wantErr: "need image mode whole-disk"},
		{name: "whole-disk mode", path: "/usr/share/layout.json", mode: "Whole-Disk"},
		// An overridden source is never read, so it is not checked.
		{name: "inline layout overrides http URL", url: "http://layouts.example.invalid/cis.json", inline: true},
		{name: "inline layout overrides A/B check", path: "/usr/share/layout.json", mode: ImageModeAB, inline: true},
		{name: "URL overrides bad image path", url: "https://layouts.example.invalid/cis.json", path: "relative/layout.json"},
		{name: "URL checked over image path", url: "http://layouts.example.invalid/cis.json", path: "/usr/share/layout.json", wantErr: "plain HTTP"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disk := DiskConfig{PartitionLayoutURL: tc.url, PartitionLayoutImagePath: tc.path}
			if tc.inline {
				disk.PartitionLayout = &PartitionLayout{}
			}
			img := ImageConfig{Mode: tc.mode}
			if tc.allowHTTP {
				img.AllowInsecureHTTP = &yes
			}
			errs := validatePartitionLayoutSources(&disk, &img)
			got := strings.Join(errs, "; ")
			if tc.wantErr == "" && got != "" {
				t.Fatalf("unexpected errors: %s", got)
			}
			if tc.wantErr != "" && !strings.Contains(got, tc.wantErr) {
				t.Fatalf("errors = %q, want %q", got, tc.wantErr)
			}
			if disk.PartitionLayoutURL != strings.TrimSpace(tc.url) {
				t.Fatalf("URL not trimmed: %q", disk.PartitionLayoutURL)
			}
		})
	}
}

func TestPartitionLayoutRequested(t *testing.T) {
	for _, tc := range []struct {
		disk DiskConfig
		want bool
	}{
		{DiskConfig{}, false},
		{DiskConfig{PartitionLayoutURL: " "}, false},
		{DiskConfig{PartitionLayout: &PartitionLayout{}}, true},
		{DiskConfig{PartitionLayoutURL: "https://layouts.example.invalid/l.json"}, true},
		{DiskConfig{PartitionLayoutImagePath: "/usr/share/l.json"}, true},
	} {
		if got := tc.disk.PartitionLayoutRequested(); got != tc.want {
			t.Errorf("PartitionLayoutRequested(%+v) = %v, want %v", tc.disk, got, tc.want)
		}
	}
}
