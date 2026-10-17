package tpm

// PCR plan, following the UAPI TPM PCR registry
// (https://uapi-group.org/specifications/specs/linux_tpm_pcr_registry/):
//
//	PCR 0-7:  firmware; read only. PCR 7 is the Secure Boot policy.
//	PCR 8, 9: GRUB commands and files. BOOTy does not extend them.
//	PCR 10:   IMA. BOOTy does not extend it.
//	PCR 11-13: systemd-stub (UKI sections, command line, sysexts).
//	PCR 14:   shim MokList. BOOTy does not extend it.
//	PCR 15:   systemd machine identity and volume keys.
//	PCR 16:   debug.
//	PCR 23:   application support. BOOTy runtime events.
//
// BOOTy extends its own events (binary, configuration, image) into one
// PCR, PCRBooty, and keeps the event order in a MeasurementLog, which the
// verifier replays. PCR 15 is not used: after a kexec handoff the target OS
// keeps the PCR values of the BOOTy boot, and systemd in the target expects
// PCR 15 to hold only its own events. PCR 23 is resettable by software, so
// a verifier must replay the event log and not trust PCR 23 alone; the
// BOOTy binary itself is measured by firmware and systemd-stub (PCR 4, 11).
// See docs/usage-security-telemetry.md#pcr-plan.
const (
	PCRFirmware    = 0  // Platform firmware measurements
	PCRFirmwareCfg = 1  // Firmware configuration
	PCRBootLoader  = 4  // Bootloader measurements
	PCRSecureBoot  = 7  // Secure Boot policy
	PCRBooty       = 23 // BOOTy runtime events (application support)
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
	case PCRBooty:
		return "BOOTy runtime events"
	default:
		return "unknown"
	}
}
