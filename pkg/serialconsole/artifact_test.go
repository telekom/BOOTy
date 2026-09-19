package serialconsole

import (
	"encoding/json"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMarshalArtifactBoundsEvidenceWithoutFailingResolution(t *testing.T) {
	resolution := Resolution{State: StateResolved, Reason: "test"}
	for i := 0; i < maxEvidenceEntries+2; i++ {
		resolution.Evidence = append(resolution.Evidence, Evidence{
			Source: SourceSysfsUART,
			Path:   "path",
			Detail: "detail",
		})
	}
	artifact := NewArtifact(&resolution, time.Unix(0, 0), nil)
	data, err := MarshalArtifact(&artifact)
	if err != nil {
		t.Fatalf("MarshalArtifact() error: %v", err)
	}
	var got Artifact
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	if len(got.Resolution.Evidence) != maxEvidenceEntries {
		t.Fatalf("evidence count = %d, want %d", len(got.Resolution.Evidence), maxEvidenceEntries)
	}
	if len(resolution.Evidence) != maxEvidenceEntries+2 {
		t.Fatal("MarshalArtifact mutated the input resolution")
	}
	if len(got.Resolution.Degraded) != 1 || got.Resolution.Degraded[0] != "evidence truncated to 32 entries" {
		t.Fatalf("degraded = %v, want truncation marker", got.Resolution.Degraded)
	}
}

func TestTruncatePreservesUTF8Boundary(t *testing.T) {
	got := truncate("界界界", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
	if len(got) > 4 || got != "界" {
		t.Fatalf("truncate = %q (%d bytes), want one complete rune", got, len(got))
	}
}
