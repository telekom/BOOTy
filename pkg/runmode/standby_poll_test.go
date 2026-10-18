//go:build linux

package runmode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
)

// queuedProvider returns one batch of commands per poll, then none.
type queuedProvider struct {
	standbyProvider
	batches [][]config.Command
}

func (p *queuedProvider) FetchCommands(context.Context) ([]config.Command, error) {
	p.fetches++
	if len(p.batches) == 0 {
		return nil, nil
	}
	b := p.batches[0]
	p.batches = p.batches[1:]
	return b, nil
}

// After a deprovision command that returns to standby, the next poll starts
// after repollAfterCommands, not after the poll interval: CAPRF queues the
// provision command right after the ACK.
func TestStandbyPollsAgainAtOnceAfterCommands(t *testing.T) {
	defer func(d time.Duration) { pollInterval = d }(pollInterval)
	pollInterval = time.Hour

	cfg := standbyConfig()
	cfg.Agent.DeprovisionSuccessAction = config.AgentDeprovisionStandby
	provider := &queuedProvider{batches: [][]config.Command{
		{{ID: "d1", Type: "deprovision"}},
		{{ID: "r1", Type: "reboot"}},
	}}
	mode := &StandbyMode{
		deps: Deps{Cfg: cfg, Client: provider},
		newDeprovisioner: func(*config.MachineConfig) standbyDeprovisioner {
			return standbyDeprovisionerFunc(func(context.Context) error { return nil })
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var reboot *RebootRequestedError
	if err := mode.Run(ctx); !errors.As(err, &reboot) {
		t.Fatalf("Run error = %v, want the reboot command without waiting for the poll interval", err)
	}
	if provider.fetches != 2 {
		t.Fatalf("fetches = %d, want 2", provider.fetches)
	}
}

func TestNextPollDelay(t *testing.T) {
	defer func(d time.Duration) { pollInterval = d }(pollInterval)
	pollInterval = 10 * time.Second

	if d := nextPollDelay(time.Now(), true); d != repollAfterCommands {
		t.Errorf("after commands: delay = %v, want %v", d, repollAfterCommands)
	}
	// A long poll that the server held for 20 s: poll again at once.
	if d := nextPollDelay(time.Now().Add(-20*time.Second), false); d != 0 {
		t.Errorf("after a long poll: delay = %v, want 0", d)
	}
	// A server that answers at once: keep the interval.
	if d := nextPollDelay(time.Now(), false); d < 9*time.Second {
		t.Errorf("after a short poll: delay = %v, want about 10s", d)
	}
}

func TestMemInfoMiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	data := "MemTotal:        2048000 kB\nMemFree:          100000 kB\nMemAvailable:    1024000 kB\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if total, available := memInfoMiB(path); total != 2000 || available != 1000 {
		t.Fatalf("memInfoMiB = %d, %d, want 2000, 1000", total, available)
	}
	if total, available := memInfoMiB(filepath.Join(t.TempDir(), "missing")); total != 0 || available != 0 {
		t.Fatalf("missing file: %d, %d, want zeros", total, available)
	}
}

// longPollProvider holds every poll like a CAPRF long poll and records the
// heartbeat times; the heartbeats come from another goroutine.
type longPollProvider struct {
	standbyProvider
	hold time.Duration

	mu         sync.Mutex
	heartbeats []time.Time
}

func (p *longPollProvider) Heartbeat(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.heartbeats = append(p.heartbeats, time.Now())
	return nil
}

func (p *longPollProvider) FetchCommands(ctx context.Context) ([]config.Command, error) {
	select {
	case <-time.After(p.hold):
	case <-ctx.Done():
	}
	return nil, nil
}

// A long poll must not delay the heartbeats: the poll holds the loop for
// several heartbeat intervals here.
func TestStandbyHeartbeatsDoNotWaitForTheLongPoll(t *testing.T) {
	defer func(h, p time.Duration) { heartbeatInterval, pollInterval = h, p }(heartbeatInterval, pollInterval)
	heartbeatInterval, pollInterval = 20*time.Millisecond, 0

	provider := &longPollProvider{hold: 100 * time.Millisecond}
	mode := &StandbyMode{deps: Deps{Cfg: standbyConfig(), Client: provider}}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := mode.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want the context deadline", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	// The first heartbeat is the readiness heartbeat before the first poll.
	if n := len(provider.heartbeats); n < 15 {
		t.Fatalf("heartbeats = %d in 600 ms with a 20 ms interval, want at least 15", n)
	}
	for i := 2; i < len(provider.heartbeats); i++ {
		if gap := provider.heartbeats[i].Sub(provider.heartbeats[i-1]); gap > 80*time.Millisecond {
			t.Fatalf("heartbeat gap %v, want about the 20 ms interval, not the 100 ms poll", gap)
		}
	}
}

// failOnceProvider fails the first loop poll and then returns a reboot.
type failOnceProvider struct {
	standbyProvider
}

func (p *failOnceProvider) FetchCommands(context.Context) ([]config.Command, error) {
	p.fetches++
	switch p.fetches {
	case 1: // readiness poll
		return nil, nil
	case 2:
		return nil, errors.New("poll failed")
	default:
		return []config.Command{{ID: "r1", Type: "reboot"}}, nil
	}
}

// A failed poll schedules the next one.
func TestStandbyPollsAgainAfterAFailedPoll(t *testing.T) {
	defer func(d time.Duration) { pollInterval = d }(pollInterval)
	pollInterval = 10 * time.Millisecond

	provider := &failOnceProvider{}
	mode := &StandbyMode{deps: Deps{Cfg: standbyConfig(), Client: provider}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var reboot *RebootRequestedError
	if err := mode.Run(ctx); !errors.As(err, &reboot) {
		t.Fatalf("Run error = %v, want the reboot command of the poll after the failed one", err)
	}
	if provider.fetches != 3 {
		t.Fatalf("fetches = %d, want 3", provider.fetches)
	}
}
