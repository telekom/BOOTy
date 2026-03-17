package tpm

// PCR register assignments for the BOOTy provisioning pipeline.
// These follow the TCG specification layout where possible:
//   PCR 0-6:   Platform firmware (measured by hardware/firmware)
//   PCR 7:     Secure Boot policy
//   PCR 8-13:  BOOTy provisioning measurements
//   PCR 14:    BOOTy identity / provisioner measurement
//   PCR 15:    OS image measurement
const (
	PCRFirmware    = 0  // Platform firmware measurements
	PCRFirmwareCfg = 1  // Firmware configuration
	PCRBootLoader  = 4  // Bootloader measurements
	PCRSecureBoot  = 7  // Secure Boot policy
	PCRBinary      = 8  // BOOTy binary integrity
	PCRImage       = 9  // OS image checksum
	PCRConfig      = 10 // Provisioning configuration hash
	PCRProvisioner = 14 // BOOTy provisioner identity
	PCROSImage     = 15 // OS image measurement (streaming)
)

// PCRDescription returns a human-readable description for a PCR index.
func PCRDescription(pcr int) string {
	switch pcr {
	case PCRFirmware:
		return "platform firmware"
	case PCRFirmwareCfg:
		return "firmware configuration"
	case PCRBootLoader:
		return "bootloader"
	case PCRSecureBoot:
		return "secure boot policy"
	case PCRBinary:
		return "BOOTy binary"
	case PCRImage:
		return "OS image checksum"
	case PCRConfig:
		return "provisioning config"
	case PCRProvisioner:
		return "provisioner identity"
	case PCROSImage:
		return "OS image streaming"
	default:
		return "unknown"
	}
}
