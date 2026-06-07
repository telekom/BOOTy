//go:build !linux

package network

import (
	"context"
	"fmt"
	"time"
)

// DHCPMode is only functional on Linux, where raw netlink/DHCP access is
// available. The non-Linux implementation keeps tests and config parsing
// portable while returning an explicit setup error.
type DHCPMode struct{}

func NewDHCPMode() *DHCPMode {
	return &DHCPMode{}
}

func (d *DHCPMode) Setup(context.Context, *Config) error {
	return fmt.Errorf("dhcp mode is only supported on Linux")
}

func (d *DHCPMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

func (d *DHCPMode) Teardown(context.Context) error {
	return nil
}
