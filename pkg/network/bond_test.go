//go:build linux

package network

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestBondModeTeardownRemovesPartiallyCreatedBond(t *testing.T) {
	originalLinkAdd := bondLinkAdd
	originalLinkByName := bondLinkByName
	originalLinkSetDown := bondLinkSetDown
	originalLinkSetBondSlave := bondLinkSetBondSlave
	originalLinkSetUp := bondLinkSetUp
	originalLinkDel := bondLinkDel
	t.Cleanup(func() {
		bondLinkAdd = originalLinkAdd
		bondLinkByName = originalLinkByName
		bondLinkSetDown = originalLinkSetDown
		bondLinkSetBondSlave = originalLinkSetBondSlave
		bondLinkSetUp = originalLinkSetUp
		bondLinkDel = originalLinkDel
	})

	var deleted string
	bondLinkAdd = func(link netlink.Link) error {
		if link.Attrs().Name != "bond0" {
			t.Fatalf("LinkAdd name = %q, want bond0", link.Attrs().Name)
		}
		return nil
	}
	bondLinkByName = func(string) (netlink.Link, error) {
		return nil, errors.New("missing test slave")
	}
	bondLinkSetDown = func(netlink.Link) error {
		t.Fatal("LinkSetDown called without a slave")
		return nil
	}
	bondLinkSetBondSlave = func(netlink.Link, *netlink.Bond) error {
		t.Fatal("LinkSetBondSlave called without a slave")
		return nil
	}
	bondLinkSetUp = func(netlink.Link) error {
		t.Fatal("LinkSetUp called without enslaved interfaces")
		return nil
	}
	bondLinkDel = func(link netlink.Link) error {
		deleted = link.Attrs().Name
		return nil
	}

	mode := &BondMode{}
	err := mode.Setup(context.Background(), &Config{BondInterfaces: "eth0"})
	if err == nil {
		t.Fatal("Setup() error = nil, want no slaves error")
	}
	if err := mode.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown() error = %v", err)
	}
	if deleted != "bond0" {
		t.Fatalf("deleted bond = %q, want bond0", deleted)
	}
}

func TestBondModeTeardownRemovesReusedBondAfterSetupFailure(t *testing.T) {
	originalLinkAdd := bondLinkAdd
	originalLinkByName := bondLinkByName
	originalLinkSetDown := bondLinkSetDown
	originalLinkSetBondSlave := bondLinkSetBondSlave
	originalLinkSetUp := bondLinkSetUp
	originalLinkDel := bondLinkDel
	t.Cleanup(func() {
		bondLinkAdd = originalLinkAdd
		bondLinkByName = originalLinkByName
		bondLinkSetDown = originalLinkSetDown
		bondLinkSetBondSlave = originalLinkSetBondSlave
		bondLinkSetUp = originalLinkSetUp
		bondLinkDel = originalLinkDel
	})

	existingBond := &netlink.Bond{LinkAttrs: netlink.LinkAttrs{Name: "bond0", Index: 42}}
	var deleted string
	bondLinkAdd = func(netlink.Link) error {
		return syscall.EEXIST
	}
	bondLinkByName = func(name string) (netlink.Link, error) {
		if name == "bond0" {
			return existingBond, nil
		}
		return nil, errors.New("missing test slave")
	}
	bondLinkSetDown = func(netlink.Link) error {
		t.Fatal("LinkSetDown called without a slave")
		return nil
	}
	bondLinkSetBondSlave = func(netlink.Link, *netlink.Bond) error {
		t.Fatal("LinkSetBondSlave called without a slave")
		return nil
	}
	bondLinkSetUp = func(netlink.Link) error {
		t.Fatal("LinkSetUp called without enslaved interfaces")
		return nil
	}
	bondLinkDel = func(link netlink.Link) error {
		deleted = link.Attrs().Name
		return nil
	}

	mode := &BondMode{}
	err := mode.Setup(context.Background(), &Config{BondInterfaces: "eth0"})
	if err == nil {
		t.Fatal("Setup() error = nil, want no slaves error")
	}
	if err := mode.Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown() error = %v", err)
	}
	if deleted != "bond0" {
		t.Fatalf("deleted bond = %q, want bond0", deleted)
	}
}
