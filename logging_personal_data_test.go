package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/logging"
)

// The personal-data table of docs/logging.md sits between these markers.
const (
	personalDataStart = "<!-- personal-data:start -->"
	personalDataEnd   = "<!-- personal-data:end -->"
)

// personalField classifies an attribute key that can hold personal data.
// Classes and data subjects are those of the T-CaaS platform logging
// contract; a key that can hold several data subjects names the one with
// the highest need for protection (end-customer, tenant-user, operator).
type personalField struct {
	class, subject, meaning string
}

// personalDataFields are the attribute keys that can hold personal data.
// The machine BOOTy provisions can be a node of a tenant cluster, so its
// names and addresses are tenant-user data.
var personalDataFields = map[string]personalField{
	"hostname":       {"customer-data", "tenant-user", "host name of the machine (CAPRF, ramdisk, BGP, standby)"},
	"serial":         {"customer-data", "tenant-user", "serial number of a disk or of the LLDP neighbor; `out-of-band status` logs a bool"},
	"mac":            {"customer-data", "tenant-user", "MAC address of a NIC or of the IPMI interface of the machine, or of a fabric router"},
	"bridge_mac":     {"customer-data", "tenant-user", "MAC address of the provisioning bridge of the machine"},
	"match_mac":      {"customer-data", "tenant-user", "MAC address that a static network config matches"},
	"ip":             {"customer-data", "tenant-user", "IP address of the machine: IPMI, provision, overlay loopback, host route"},
	"addr":           {"customer-data", "tenant-user", "IP address of the machine (DHCP lease, static) or of a BGP peer"},
	"address":        {"customer-data", "tenant-user", "IP address of the machine (static) or of a BGP peer"},
	"ciaddr":         {"customer-data", "tenant-user", "client IP address of a DHCP message"},
	"static_ip":      {"customer-data", "tenant-user", "static IP address of the machine"},
	"provision_ip":   {"customer-data", "tenant-user", "provisioning IP address of the machine"},
	"overlay_ip":     {"customer-data", "tenant-user", "overlay loopback IP address of the machine"},
	"underlay_ip":    {"customer-data", "tenant-user", "underlay loopback IP address of the machine"},
	"vtep":           {"customer-data", "tenant-user", "VTEP IP address of the machine or of a fabric peer"},
	"vteps":          {"customer-data", "tenant-user", "VTEP IP addresses of an Ethernet segment"},
	"withdrawn_vtep": {"customer-data", "tenant-user", "VTEP IP address whose routes were withdrawn"},
	"data":           {"customer-data", "tenant-user", "one line of a network state dump (`debug`): addresses, MACs and names of the machine"},
	"remote":         {"customer-data", "tenant-user", "address and port of the rescue SSH client"},
	"principal":      {"tenant-pii", "tenant-user", "user name that a rescue SSH client logs in or tries to log in as"},
	"subject":        {"tenant-pii", "tenant-user", "OIDC subject of an opkssh rescue SSH login (an account ID or e-mail address)"},
	"key":            {"tenant-pii", "tenant-user", "fingerprint of the SSH key of a rescue login; also a vars key name"},
	"command":        {"content", "tenant-user", "command line of a machine or post-provision command of the config, values of secret flags removed"},
	"params":         {"content", "tenant-user", "per-host kernel parameters; for NIC firmware a count"},
}

// nonPersonalFields are the attribute keys that hold no personal data:
// states, counts, durations, sizes, paths, device and interface names,
// names and addresses of the data-center fabric and of platform services
// (gateway, BGP neighbor, NTP, DNS, CAPRF and image URLs), and the error.
var nonPersonalFields = strings.Fields(`
	MACHINE_SERIAL_CONSOLE action match_name tier also array asn atomic attempt attempts authenticated backoff baud
	bfd_receive_ms bfd_transmit_ms bgp_hold bgp_interfaces bgp_keepalive blockSize blocks bond_interfaces boot
	booty bridge budget build bytes candidates cert certSHA256 changes chassisID check checks cidr cipher clock
	cmd cmdID code component compliant conflicts console controller corrected_by count cpus current daemon
	datasource dcgw_ips delay destination detail details detection_time dev_nodes device devices dhcpCount diag
	diff difference dir direction disk disk1 disk2 disks dns_resolver driver dropped dst duration efi_var
	enrollSlot entries entry err error esi evidence evpn exit_code expire expiresIn export_statements failed
	failedStep fallback_blocks fallback_nsid families family fdb file filesystem findings flag floor format from
	frr.conf fs fsType gateway got gw handoff hardened headerBytes hold host_key id idle iface image image_count
	images import_statements include index initrd interface internal ipmi_subnet issuer iteration keepalive
	keepalive_every kernel keys label leaf_asn leftAlone len level line link linkIndex links lldp_neighbors loader
	localASN local_asn luksUUID lv majors managed mapped marker matches max maxFrameSize max_skew memAvailableMiB
	memTotalMiB mem_available_mib member members message method mgmtAddresses min_available_mib min_disk_size_gb
	minSizeBytes minSizeGB mirroringMode mode module modules mount mountpoint mtu multihop multiplier name native
	neighbor new nexthop nexthops nic nics nsid ntp num numVFs nvme of offset old onDrift opkssh osFamily
	outer_format output overlay_aggregate overlay_subnet panic param parent parsed partition partitions password
	path payload_format pci pci_devices peak_used_mib peer phase pid port portDescription portID portIDSubtype
	prefix proc_partitions profile provision_vni pubkey punched_zero_bytes pv quick ranges rd readOnly reason
	reboot recoverySlot ref remoteASN report reportPath required reset_within resolvers response_time result root
	rootHash rootLV route router routerID row run rx search sectors secureBoot selected selectedBy seq server
	servers set_back_by set_name setting severity sha256 signature signing sink size sizeBytes sizeGB sizeMB
	sizePct size_mib skew skew_seconds skip_reason skipped slave source source_partition src state statement
	static_gateway status step successAction sys_entries sysext systemName table target time timeout to total
	total_mib tpm2PCRs tpm2PublicKeyPCRs ttl tunnel_types tx type udev_name uefi unchanged_bytes underlay
	underlay_subnet unit unmountError unmounted uploaded uptime url valid value var variable vendor version vg
	via vlan vni volumes vpn_rt vrf vrf_table_id vxlan wait waited want writable written_bytes
`)

// loggerWithKeys returns the keys that .With adds to a logger: they are on
// every line of that logger, at every level.
func loggerWithKeys(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	var keys []string
	for _, f := range shippedGoFiles(t, fset) {
		eachCall(f.file, func(fn *ast.FuncDecl, call *ast.CallExpr) {
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "With" {
				return
			}
			if recv := types.ExprString(sel.X); !loggerReceiver.MatchString(recv) && recv != "slog.Default()" {
				return
			}
			k, resolved := pairKeys(call.Args, call.Ellipsis.IsValid(), fn, localStrings(f.file, fn))
			if !resolved {
				t.Errorf("%s: logger attributes that the scan can not resolve; use literal keys", fset.Position(call.Pos()))
			}
			keys = append(keys, k...)
		})
	}
	return keys
}

// redactedByKey reports whether the console handler replaces the value of
// the key with [REDACTED].
func redactedByKey(key string) bool {
	var buf bytes.Buffer
	slog.New(logging.NewHandler(&buf, logging.FormatText, slog.LevelDebug)).Info("probe", key, "probe-value")
	return !strings.Contains(buf.String(), "probe-value")
}

func TestLoggingDocPersonalData(t *testing.T) {
	levels := map[string][]string{} // key: levels it is logged at
	for _, c := range logCalls(t) {
		for _, k := range c.keys {
			if !slices.Contains(levels[k], c.level) {
				levels[k] = append(levels[k], c.level)
			}
		}
	}
	for _, k := range loggerWithKeys(t) {
		levels[k] = []string{"ERROR", "WARN", "INFO", "DEBUG"}
	}

	for k := range personalDataFields {
		if slices.Contains(nonPersonalFields, k) {
			t.Errorf("attribute key %q is in personalDataFields and nonPersonalFields", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(levels)) {
		if _, ok := personalDataFields[k]; !ok && !slices.Contains(nonPersonalFields, k) {
			t.Errorf("attribute key %q is not classified: add it to personalDataFields (with class and data subject) or to nonPersonalFields in logging_personal_data_test.go", k)
		}
	}
	for _, k := range append(slices.Collect(maps.Keys(personalDataFields)), nonPersonalFields...) {
		if levels[k] == nil {
			t.Errorf("attribute key %q is classified in logging_personal_data_test.go but no log call uses it; remove it", k)
		}
	}
	if t.Failed() {
		return
	}
	updateDocBlock(t, personalDataStart, personalDataEnd, renderPersonalData(levels))
}

// renderPersonalData renders the table of the personal-data fields.
func renderPersonalData(levels map[string][]string) string {
	order := []string{"ERROR", "WARN", "INFO", "DEBUG"}
	var b strings.Builder
	b.WriteString(personalDataStart + "\n")
	b.WriteString("<!-- Generated by TestLoggingDocPersonalData (UPDATE_LOGGING_DOC=1). Do not edit. -->\n\n")
	b.WriteString("| Field | Class | Data subject | Levels | Logged at `info` (default) | Value redacted | Meaning |\n")
	b.WriteString("|-------|-------|--------------|--------|----------------------------|----------------|---------|\n")
	for _, k := range slices.Sorted(maps.Keys(personalDataFields)) {
		f := personalDataFields[k]
		l := slices.Clone(levels[k])
		slices.SortFunc(l, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) })
		atInfo, redacted := "yes", "no"
		if slices.Equal(l, []string{"DEBUG"}) {
			atInfo = "no, `debug` only"
		}
		if redactedByKey(k) {
			redacted = "yes"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s | %s | %s |\n", k, f.class, f.subject, strings.Join(l, ", "), atInfo, redacted, f.meaning)
	}
	b.WriteString("\n" + personalDataEnd)
	return b.String()
}
