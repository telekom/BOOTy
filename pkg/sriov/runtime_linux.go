//go:build linux

package sriov

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// KernelRuntime is the RuntimeOps of the running kernel: sysfs for
// sriov_numvfs, devlink for the eSwitch mode and rtnetlink for the VF
// attributes. These interfaces are the same for every SR-IOV driver.
type KernelRuntime struct {
	// SysNet is the /sys/class/net directory.
	SysNet string
}

// NewKernelRuntime returns the RuntimeOps of the running kernel.
func NewKernelRuntime() *KernelRuntime {
	return &KernelRuntime{SysNet: "/sys/class/net"}
}

// SetNumVFs implements RuntimeOps.
func (k *KernelRuntime) SetNumVFs(pf *PF, n int) error {
	path := filepath.Join(k.SysNet, pf.Interface, "device", "sriov_numvfs")
	if err := os.WriteFile(path, []byte(strconv.Itoa(n)), 0o200); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// SetEswitch implements RuntimeOps.
func (k *KernelRuntime) SetEswitch(pf *PF, mode string) error {
	var m uint16
	switch mode {
	case EswitchLegacy:
		m = nl.DEVLINK_ESWITCH_MODE_LEGACY
	case EswitchSwitchdev:
		m = nl.DEVLINK_ESWITCH_MODE_SWITCHDEV
	default:
		return fmt.Errorf("unknown eSwitch mode %q", mode)
	}
	_, err := devlinkRequest(nl.DEVLINK_CMD_ESWITCH_SET, pf.Bus, pf.PCIAddress,
		nl.NewRtAttr(nl.DEVLINK_ATTR_ESWITCH_MODE, nl.Uint16Attr(m)))
	return err
}

// SetVF implements RuntimeOps.
func (k *KernelRuntime) SetVF(pf *PF, index int, s *VFSettings, mac string) error {
	link, err := netlink.LinkByName(pf.Interface)
	if err != nil {
		return fmt.Errorf("link %s: %w", pf.Interface, err)
	}
	for _, step := range vfSteps(s, mac) {
		if err := step.set(link, index); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

type vfStep struct {
	name string
	set  func(link netlink.Link, vf int) error
}

// vfSteps lists the rtnetlink calls for the set attributes, in the order
// of "ip link set <pf> vf N": MAC, VLAN and QoS, rate, spoof check, trust,
// link state.
func vfSteps(s *VFSettings, mac string) []vfStep {
	var steps []vfStep
	if mac != "" {
		steps = append(steps, vfStep{"mac", func(l netlink.Link, vf int) error {
			hw, err := net.ParseMAC(mac)
			if err != nil {
				return fmt.Errorf("parse MAC: %w", err)
			}
			return wrapNL(netlink.LinkSetVfHardwareAddr(l, vf, hw))
		}})
	}
	if s.VLAN != nil || s.QoS != nil {
		vlan, qos := deref(s.VLAN), deref(s.QoS)
		steps = append(steps, vfStep{"vlan", func(l netlink.Link, vf int) error {
			return wrapNL(netlink.LinkSetVfVlanQos(l, vf, vlan, qos))
		}})
	}
	if s.MinTxRate != nil || s.MaxTxRate != nil {
		minRate, maxRate := deref(s.MinTxRate), deref(s.MaxTxRate)
		steps = append(steps, vfStep{"rate", func(l netlink.Link, vf int) error {
			return wrapNL(netlink.LinkSetVfRate(l, vf, minRate, maxRate))
		}})
	}
	if s.SpoofChk != nil {
		on := *s.SpoofChk
		steps = append(steps, vfStep{"spoofchk", func(l netlink.Link, vf int) error {
			return wrapNL(netlink.LinkSetVfSpoofchk(l, vf, on))
		}})
	}
	if s.Trust != nil {
		on := *s.Trust
		steps = append(steps, vfStep{"trust", func(l netlink.Link, vf int) error {
			return wrapNL(netlink.LinkSetVfTrust(l, vf, on))
		}})
	}
	if s.LinkState != "" {
		state := linkStateValue(s.LinkState)
		steps = append(steps, vfStep{"state", func(l netlink.Link, vf int) error {
			return wrapNL(netlink.LinkSetVfState(l, vf, state))
		}})
	}
	return steps
}

func wrapNL(err error) error {
	if err != nil {
		return fmt.Errorf("rtnetlink: %w", err)
	}
	return nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func linkStateValue(s string) uint32 {
	switch s {
	case "enable":
		return netlink.VF_LINK_STATE_ENABLE
	case "disable":
		return netlink.VF_LINK_STATE_DISABLE
	}
	return netlink.VF_LINK_STATE_AUTO
}

// InUse implements RuntimeOps. A port is in use when it has a master
// device (bond, bridge, VRF), an address other than IPv6 link-local, or a
// route through it to a destination other than IPv6 link-local, as the
// BGP unnumbered sessions of the ramdisk install.
func (k *KernelRuntime) InUse(pf *PF) (inUse bool, reason string) {
	link, err := netlink.LinkByName(pf.Interface)
	if err != nil {
		return true, fmt.Sprintf("cannot read the link: %v", err)
	}
	if link.Attrs().MasterIndex != 0 {
		return true, "the port has a master device"
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return true, fmt.Sprintf("cannot read the addresses: %v", err)
	}
	for i := range addrs {
		if !addrs[i].IP.IsLinkLocalUnicast() {
			return true, "the port has address " + addrs[i].IPNet.String()
		}
	}
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return true, fmt.Sprintf("cannot read the routes: %v", err)
	}
	idx := link.Attrs().Index
	for i := range routes {
		if routeUses(&routes[i], idx) && forwardingRoute(&routes[i]) {
			return true, "a route goes through the port"
		}
	}
	return false, ""
}

// forwardingRoute reports a unicast route to a destination other than
// IPv6 link-local; the kernel adds link-local and multicast routes to every
// port that is up.
func forwardingRoute(r *netlink.Route) bool {
	if r.Type != unix.RTN_UNICAST {
		return false
	}
	return r.Dst == nil || (!r.Dst.IP.IsLinkLocalUnicast() && !r.Dst.IP.IsMulticast())
}

func routeUses(r *netlink.Route, idx int) bool {
	if r.LinkIndex == idx {
		return true
	}
	for _, nh := range r.MultiPath {
		if nh.LinkIndex == idx {
			return true
		}
	}
	return false
}
