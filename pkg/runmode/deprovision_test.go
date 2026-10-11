//go:build linux

package runmode

import (
	"context"
	"errors"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestDeprovisionModeExitParity(t *testing.T) {
	wipeErr := errors.New("wipe failed")
	tests := []struct {
		name         string
		legacyCompat bool
		deprovErr    error
		wantPowerOff bool
		wantErr      error
	}{
		{name: "success reboots by default"},
		{name: "success powers off in legacy compat", legacyCompat: true, wantPowerOff: true},
		{name: "failure returns error by default", deprovErr: wipeErr, wantErr: wipeErr},
		{name: "failure waits in legacy compat", legacyCompat: true, deprovErr: wipeErr, wantErr: wipeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			waited := false
			m := &DeprovisionMode{
				deps: Deps{Cfg: &config.MachineConfig{Mode: "deprovision", LegacyCompat: tt.legacyCompat}},
				newDeprovisioner: func() standbyDeprovisioner {
					return standbyDeprovisionerFunc(func(context.Context) error {
						if tt.deprovErr != nil && tt.legacyCompat {
							// Run must block until the context ends.
							waited = true
							cancel()
						}
						return tt.deprovErr
					})
				},
			}
			defer cancel()

			err := m.Run(ctx)

			var powerOff *PowerOffRequestedError
			if got := errors.As(err, &powerOff); got != tt.wantPowerOff {
				t.Fatalf("Run() = %v, want power off request %v", err, tt.wantPowerOff)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("Run() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && !tt.wantPowerOff && err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}
			if tt.legacyCompat && tt.deprovErr != nil && !waited {
				t.Fatal("legacy compat failure did not wait for the context")
			}
		})
	}
}
