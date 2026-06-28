//go:build linux && e2e_boot

package integration

import (
	"reflect"
	"testing"
	"time"
)

func TestDockerLogsArgsUsesSinceBeforeTail(t *testing.T) {
	since := time.Date(2026, 6, 25, 12, 34, 56, 789, time.FixedZone("CEST", 2*60*60))

	got := dockerLogsArgs("booty-test", "25", since)
	want := []string{
		"logs",
		"--since",
		"2026-06-25T10:34:56.000000789Z",
		"--tail",
		"25",
		"booty-test",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerLogsArgs() = %#v, want %#v", got, want)
	}
}
