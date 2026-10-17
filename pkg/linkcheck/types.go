// Package linkcheck runs the cabling checks of check mode on every
// physical NIC: the LLDP neighbors against the expected cabling, and the
// link, PCIe and transceiver state, and writes one result document.
package linkcheck

import (
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
	"github.com/telekom/BOOTy/pkg/network/lldp"
)

// Schema is the version of the result document.
const Schema = "booty.check/v1"

// Document is the check result that the sinks send.
type Document struct {
	Schema     string               `json:"schema"`
	Mode       string               `json:"mode"`
	Profiles   []string             `json:"profiles"`
	StartedAt  time.Time            `json:"startedAt"`
	FinishedAt time.Time            `json:"finishedAt"`
	Host       Host                 `json:"host"`
	Status     string               `json:"status"` // pass, warn or fail
	Checks     []health.CheckResult `json:"checks"`
	Links      []Link               `json:"links,omitempty"`
	// Diff is the expected cabling diff in the bm4x ConnectionReport form.
	Diff []ConnectionEntry `json:"diff,omitempty"`
}

// Host identifies the machine. The serial number is left out, like in the
// hardware inventory.
type Host struct {
	Hostname     string `json:"hostname"`
	Product      string `json:"product,omitempty"`
	UUID         string `json:"uuid,omitempty"`
	BootyVersion string `json:"bootyVersion,omitempty"`
}

// Link is what BOOTy knows about one physical NIC.
type Link struct {
	Ifname   string               `json:"ifname"`
	MAC      string               `json:"mac"`
	PCI      string               `json:"pci,omitempty"`
	Driver   string               `json:"driver,omitempty"`
	Carrier  bool                 `json:"carrier"`
	LLDP     *lldp.Neighbor       `json:"lldp,omitempty"`
	Expected *config.ExpectedLink `json:"expected,omitempty"`
}

// Endpoint is one end of a connection in the bm4x ConnectionReport
// (ConnectionEntry.Endpoint). deviceName and deviceStatus, which only
// NetBox knows, are left out.
type Endpoint struct {
	Hostname      string `json:"hostname,omitempty"`
	InterfaceName string `json:"interfaceName,omitempty"`
}

// ConnectionEntry is one mismatch in the bm4x ConnectionReport.diff form:
// a is the host NIC, bactual the switch port that LLDP shows, bexpected
// the expected one. The names follow the bm4x JSON: Jackson writes the
// Lombok getters getBActual and getBExpected as "bactual" and "bexpected".
type ConnectionEntry struct {
	A         Endpoint `json:"a"`
	BActual   Endpoint `json:"bactual"`
	BExpected Endpoint `json:"bexpected"`
}
