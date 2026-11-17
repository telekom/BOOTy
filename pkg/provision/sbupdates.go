//go:build linux

package provision

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/efi"
)

// Secure Boot database updates of the image (#976,
// docs/disk-encryption.md#secure-boot-database-updates). Ubuntu's
// secureboot-db.service runs sbkeysync on the first boot, which appends the
// signed updates in sbUpdatesDir to KEK, db and dbx. The firmware measures
// those variables into PCR 7 at boot, so that write would move PCR 7 one
// boot after the first-boot unit bound the volume to it. apply-secureboot-
// updates makes the same writes while provisioning, before the firmware
// reboot: the first boot of the installed system measures the updated
// variables and binds to its final PCR 7, and sbkeysync finds nothing left.
const (
	sbUpdatesDir = "/usr/share/secureboot/updates"
	// efiImageSecurityDatabaseGUID owns db and dbx.
	efiImageSecurityDatabaseGUID = "d719b2cb-3d3a-4596-a3bc-dad00e67656f"
	// sbAppendAttrs are the attributes sbkeysync writes with: non-volatile,
	// boot service and runtime access, time-based authenticated write,
	// append write.
	sbAppendAttrs = 0x01 | 0x02 | 0x04 | 0x20 | 0x40
)

// sbUpdateVars are the keystore directories of sbkeysync (PK it leaves
// alone without --pk) in write order: a KEK update signs later db and dbx
// updates.
var sbUpdateVars = []struct{ name, guid string }{
	{"KEK", efiGlobalGUID}, {"db", efiImageSecurityDatabaseGUID}, {"dbx", efiImageSecurityDatabaseGUID},
}

// writeEFIVar is efi.EFIVarReader.WriteVar; tests fake a firmware refusal.
var writeEFIVar = (*efi.EFIVarReader).WriteVar

// efiCertTypePKCS7 is EFI_CERT_TYPE_PKCS7_GUID in its byte order on disk.
var efiCertTypePKCS7 = []byte{0x9d, 0xd2, 0xaf, 0x4a, 0xdf, 0x68, 0xee, 0x49, 0x8a, 0xa9, 0x34, 0x7d, 0x37, 0x56, 0x65, 0xa7}

// applySecureBootUpdates is the apply-secureboot-updates step: with disk
// encryption only, where PCR 7 matters.
func (o *Orchestrator) applySecureBootUpdates(context.Context) error {
	if o.encryption == nil {
		return nil
	}
	if !config.BoolOr(o.cfg.Provision.SecureBoot.ApplyDBXUpdates, true) {
		o.log.Warn("Secure Boot updates of the image are not applied (SECUREBOOT_APPLY_DBX_UPDATES=false); if secureboot-db applies one on the first boot, PCR 7 changes after the binding")
		return nil
	}
	// os.Root: machine files keep symlinks, which must not lead out of the
	// target (TestNoHostWriterRunsAfterCopyMachineFiles).
	root, err := os.OpenRoot(newroot)
	if err != nil {
		return fmt.Errorf("open the target root: %w", err)
	}
	defer root.Close() //nolint:errcheck // read only
	return applySecureBootUpdateFiles(o.log, root.FS(), efi.NewEFIVarReader(efivarsDir))
}

// applySecureBootUpdateFiles appends every update file of
// sbUpdatesDir/{KEK,db,dbx} in the target root whose signature entries are
// not all in the variable yet, like sbkeysync.
// A firmware refusal (the write to efivarfs fails) is a warning: the
// firmware refuses sbkeysync the same way on the first boot, so PCR 7
// stays. What leaves BOOTy unable to decide (unreadable files or
// variables, a local efivarfs error before the write) fails the step; a
// malformed list in the variable is a warning, as sbkeysync reads past it.
func applySecureBootUpdateFiles(log *slog.Logger, target fs.FS, vars *efi.EFIVarReader) error {
	for _, v := range sbUpdateVars {
		dir := path.Join(strings.TrimPrefix(sbUpdatesDir, "/"), v.name)
		files, err := fs.ReadDir(target, dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read the Secure Boot updates of the image: %w", err)
		}
		for _, f := range files { // sorted by name
			rel := path.Join(dir, f.Name())
			if strings.HasPrefix(f.Name(), ".") {
				continue
			}
			// sbkeysync follows symlinks; os.Root follows those that stay
			// in the target. A dangling one sbkeysync skips too; one that
			// leaves the target (absolute) BOOTy cannot follow, so it fails.
			fi, err := fs.Stat(target, rel)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read Secure Boot update /%s: %w", rel, err)
			}
			if !fi.Mode().IsRegular() {
				continue
			}
			if err := applySecureBootUpdateFile(log, target, vars, v.name, v.guid, rel); err != nil {
				return err
			}
		}
	}
	return nil
}

func applySecureBootUpdateFile(log *slog.Logger, target fs.FS, vars *efi.EFIVarReader, name, guid, rel string) error {
	file := "/" + rel
	data, err := fs.ReadFile(target, rel)
	if err != nil {
		return fmt.Errorf("read Secure Boot update %s: %w", file, err)
	}
	update, err := authVarSignatures(data)
	if err != nil {
		// sbkeysync skips such a file too.
		log.Warn("Secure Boot update skipped: not a signed signature list", "variable", name, "file", file, "error", err)
		return nil
	}
	cur, err := vars.ReadVar(name + "-" + guid)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", name, err)
	}
	have, err := signatureEntries(cur)
	if err != nil {
		// sbkeysync ignores what does not parse in the variable too. Fewer
		// entries only make BOOTy write an update that sbkeysync might
		// skip, never skip one that sbkeysync writes.
		log.Warn("the firmware variable has a malformed signature list: comparing with the entries before it",
			"variable", name, "entries", len(have), "error", err)
	}
	present := make(map[string]bool, len(have))
	for _, e := range have {
		present[e] = true
	}
	missing := 0
	for _, e := range update {
		if !present[e] {
			missing++
		}
	}
	if missing == 0 {
		log.Info("Secure Boot update already applied", "variable", name, "file", file, "entries", len(update))
		return nil
	}
	if err := writeEFIVar(vars, name+"-"+guid, sbAppendAttrs, data); err != nil {
		// Only the write itself reaches the firmware. Clearing the
		// immutable flag or opening the efivarfs file failed locally, and
		// sbkeysync may succeed on the first boot: that fails the step.
		var pe *fs.PathError
		if !errors.As(err, &pe) || pe.Op != "write" {
			return fmt.Errorf("write %s: %w", name, err)
		}
		log.Warn("the firmware refused the Secure Boot update; secureboot-db of the image gets the same answer, so PCR 7 stays",
			"variable", name, "file", file, "error", err)
		return nil
	}
	log.Info("Secure Boot update applied", "variable", name, "file", file, "entries", len(update), "new", missing)
	return nil
}

// authVarSignatures returns the signature entries of an
// EFI_VARIABLE_AUTHENTICATION_2 update: EFI_TIME (16 bytes), a
// WIN_CERTIFICATE_UEFI_GUID with a PKCS7 signature whose dwLength covers
// it, then the signature lists.
func authVarSignatures(b []byte) ([]string, error) {
	const timeSize, certHdrSize = 16, 8 + 16
	if len(b) < timeSize+certHdrSize {
		return nil, fmt.Errorf("%d bytes: too short for EFI_VARIABLE_AUTHENTICATION_2", len(b))
	}
	if !bytes.Equal(b[timeSize+8:timeSize+certHdrSize], efiCertTypePKCS7) {
		return nil, errors.New("the certificate type is not PKCS7")
	}
	n := binary.LittleEndian.Uint32(b[timeSize:])
	if n < certHdrSize || uint64(n) > uint64(len(b)-timeSize) {
		return nil, fmt.Errorf("WIN_CERTIFICATE length %d out of range", n)
	}
	return signatureEntries(b[timeSize:][n:])
}

// signatureEntries returns the entries of a series of EFI_SIGNATURE_LISTs,
// each the signature type and the signature data without the owner GUID.
// sbkeysync compares less (SHA-256 data or the X.509 serial number, any
// type; other types not at all), so an entry found here is one that
// sbkeysync finds too. On an error it returns the entries before the
// malformed list.
func signatureEntries(b []byte) ([]string, error) {
	const listHdrSize, ownerSize = 28, 16
	var out []string
	for len(b) > 0 {
		if len(b) < listHdrSize {
			return out, fmt.Errorf("signature list header truncated at %d bytes", len(b))
		}
		size := binary.LittleEndian.Uint32(b[16:])
		hdr := binary.LittleEndian.Uint32(b[20:])
		sig := binary.LittleEndian.Uint32(b[24:])
		if size < listHdrSize || uint64(size) > uint64(len(b)) || hdr > size-listHdrSize ||
			sig <= ownerSize || (size-listHdrSize-hdr)%sig != 0 {
			return out, fmt.Errorf("malformed signature list (size %d, header %d, signature %d)", size, hdr, sig)
		}
		typ := string(b[:16])
		for body := b[listHdrSize+hdr : size]; len(body) > 0; body = body[sig:] {
			out = append(out, typ+string(body[ownerSize:sig]))
		}
		b = b[size:]
	}
	return out, nil
}
