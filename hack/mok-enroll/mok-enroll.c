// SPDX-License-Identifier: Apache-2.0
//
// mok-enroll.efi writes the per-host module signing certificate of BOOTy
// into the shim MokList variable without console interaction.
//
// BOOTy creates a boot entry for this program whose optional data is one
// EFI_SIGNATURE_LIST (an X.509 certificate with the BOOTy owner GUID) and
// points BootNext at it. The program
//   1. checks the signature list,
//   2. reads MokList, drops every list owned by BOOTy (the certificate of an
//      earlier provisioning) and appends the new one,
//   3. writes MokList as NV+BS, the attributes shim requires (shim rejects a
//      MokList with the runtime attribute, as the OS could have written it),
//   4. deletes its own boot entry, records the result in the NV+BS+RT
//      variable BOOTyMokEnroll so the OS can check it, and resets.
//
// MokList is a boot-services-only variable: the OS cannot write it, only
// code that runs before ExitBootServices. This program is unsigned, so the
// firmware runs it only while Secure Boot is off, and it refuses to run when
// SecureBoot reads 1. Never sign it: with Secure Boot on, OS root could
// create a boot entry for it and add any certificate to MokList. See
// docs/module-signing.md.
#include <efi.h>
#include <efilib.h>

static EFI_GUID shim_lock = {0x605dab50, 0xe046, 0x4300, {0xab, 0xb6, 0x3d, 0xd8, 0x10, 0xdd, 0x8b, 0x23}};
static EFI_GUID cert_x509 = {0xa5c059a1, 0x94e4, 0x4aa7, {0x87, 0xb5, 0xab, 0x15, 0x5c, 0x2b, 0xf0, 0x72}};
static EFI_GUID booty_owner = {0x11378d35, 0xe20e, 0x4116, {0x94, 0xd7, 0x75, 0x8d, 0x75, 0x40, 0xa1, 0x7f}};
static EFI_GUID global_var = EFI_GLOBAL_VARIABLE;

#define MOK_ATTRS (EFI_VARIABLE_NON_VOLATILE | EFI_VARIABLE_BOOTSERVICE_ACCESS)
#define STATUS_ATTRS (MOK_ATTRS | EFI_VARIABLE_RUNTIME_ACCESS)
#define ESL_HDR 28 // SignatureType + SignatureListSize + SignatureHeaderSize + SignatureSize

typedef struct {
	EFI_GUID type;
	UINT32 list_size;
	UINT32 header_size;
	UINT32 sig_size;
} esl_t;

// booty_owned reports whether the signature list at p (n bytes, already
// bounds-checked by the caller) is an X.509 list of exactly one signature
// whose owner is BOOTy. A list with more signatures is never BOOTy's, so
// merge cannot drop the certificates of other owners with it.
static BOOLEAN booty_owned(const UINT8 *p, UINTN n)
{
	const esl_t *l = (const esl_t *)p;
	if (n < ESL_HDR || l->header_size > n - ESL_HDR || l->sig_size != n - ESL_HDR - l->header_size ||
	    l->sig_size < sizeof(EFI_GUID))
		return FALSE;
	return CompareMem(&l->type, &cert_x509, sizeof(EFI_GUID)) == 0 &&
	       CompareMem(p + ESL_HDR + l->header_size, &booty_owner, sizeof(EFI_GUID)) == 0;
}

// valid_payload accepts exactly one BOOTy-owned X.509 signature list.
static BOOLEAN valid_payload(const UINT8 *p, UINT32 n)
{
	const esl_t *l = (const esl_t *)p;
	if (p == NULL || n < ESL_HDR + sizeof(EFI_GUID) + 1 || n > 64 * 1024)
		return FALSE;
	return l->list_size == n && l->header_size == 0 && l->sig_size == n - ESL_HDR && booty_owned(p, n);
}

static void set_status(CHAR8 *msg)
{
	uefi_call_wrapper(RT->SetVariable, 5, L"BOOTyMokEnroll", &booty_owner, STATUS_ATTRS,
			  strlena(msg), msg);
}

static EFI_STATUS fail(CHAR16 *what, CHAR8 *status, EFI_STATUS s)
{
	Print(L"BOOTy mok-enroll: FAILED: %s: %r\n", what, s);
	set_status(status);
	uefi_call_wrapper(BS->Stall, 1, 3 * 1000 * 1000);
	return s;
}

// merge returns MokList with every BOOTy-owned list replaced by payload.
static EFI_STATUS merge(UINT8 *old, UINTN old_size, UINT8 *payload, UINT32 n, UINT8 **out, UINTN *out_size)
{
	UINT8 *buf = AllocatePool(old_size + n);
	UINTN m = 0;
	if (buf == NULL)
		return EFI_OUT_OF_RESOURCES;
	for (UINTN off = 0; off < old_size;) {
		const esl_t *l = (const esl_t *)(old + off);
		// Every list kept must be well formed too: one malformed list stops
		// shim from reading the certificates after it.
		if (old_size - off < ESL_HDR || l->list_size < ESL_HDR || l->list_size > old_size - off ||
		    l->header_size > l->list_size - ESL_HDR || l->sig_size < sizeof(EFI_GUID) ||
		    l->list_size - ESL_HDR - l->header_size == 0 ||
		    (l->list_size - ESL_HDR - l->header_size) % l->sig_size != 0) {
			FreePool(buf);
			return EFI_VOLUME_CORRUPTED;
		}
		if (booty_owned(old + off, l->list_size))
			Print(L"BOOTy mok-enroll: removing the previous BOOTy certificate\n");
		else {
			CopyMem(buf + m, old + off, l->list_size);
			m += l->list_size;
		}
		off += l->list_size;
	}
	CopyMem(buf + m, payload, n);
	*out = buf;
	*out_size = m + n;
	return EFI_SUCCESS;
}

// secure_boot_on reports whether SecureBoot reads 1. Firmware without Secure
// Boot support has no SecureBoot variable and loads any image anyway; with
// Secure Boot on, UEFI requires the variable.
static BOOLEAN secure_boot_on(void)
{
	UINT8 sb = 0;
	UINTN size = sizeof(sb);
	EFI_STATUS s = uefi_call_wrapper(RT->GetVariable, 5, L"SecureBoot", &global_var, NULL, &size, &sb);
	return !EFI_ERROR(s) && sb != 0;
}

// delete_own_entry deletes Boot<BootCurrent> if it is the helper entry that
// BOOTy created (EFI_LOAD_OPTION: UINT32 attributes, UINT16 file path
// length, then the description).
static void delete_own_entry(void)
{
	static const CHAR16 label[] = L"booty-mok-enroll";
	UINT8 opt[6 + sizeof(label)];
	UINT16 current = 0;
	UINTN size = sizeof(current);
	CHAR16 name[] = L"Boot0000";
	EFI_STATUS s = uefi_call_wrapper(RT->GetVariable, 5, L"BootCurrent", &global_var, NULL, &size, &current);
	if (!EFI_ERROR(s)) {
		for (int i = 0; i < 4; i++)
			name[7 - i] = L"0123456789ABCDEF"[(current >> (4 * i)) & 0xf];
		size = sizeof(opt);
		s = uefi_call_wrapper(RT->GetVariable, 5, name, &global_var, NULL, &size, opt);
		// A longer entry returns EFI_BUFFER_TOO_SMALL: the label is followed
		// by the file path and the payload.
		if (s == EFI_BUFFER_TOO_SMALL) {
			UINT8 *full = AllocatePool(size);
			s = full == NULL ? EFI_OUT_OF_RESOURCES
					 : uefi_call_wrapper(RT->GetVariable, 5, name, &global_var, NULL, &size, full);
			if (!EFI_ERROR(s))
				CopyMem(opt, full, sizeof(opt));
			if (full != NULL)
				FreePool(full);
		}
		if (!EFI_ERROR(s) && (size < sizeof(opt) || CompareMem(opt + 6, label, sizeof(label)) != 0))
			s = EFI_NOT_FOUND;
		if (!EFI_ERROR(s))
			s = uefi_call_wrapper(RT->SetVariable, 5, name, &global_var, 0, 0, NULL);
	}
	if (EFI_ERROR(s))
		Print(L"BOOTy mok-enroll: cannot delete its boot entry: %r\n", s);
}

EFI_STATUS efi_main(EFI_HANDLE image, EFI_SYSTEM_TABLE *systab)
{
	EFI_LOADED_IMAGE *li = NULL;
	UINT8 *old = NULL, *merged = NULL;
	UINTN old_size = 0, merged_size = 0;
	UINT32 attrs = 0;
	EFI_STATUS s;

	InitializeLib(image, systab);
	if (secure_boot_on())
		return fail(L"Secure Boot is on, MokList left unchanged", (CHAR8 *)"error secure-boot", EFI_ACCESS_DENIED);
	s = uefi_call_wrapper(BS->HandleProtocol, 3, image, &LoadedImageProtocol, (void **)&li);
	if (EFI_ERROR(s))
		return fail(L"loaded image protocol", (CHAR8 *)"error loaded-image", s);
	if (!valid_payload(li->LoadOptions, li->LoadOptionsSize))
		return fail(L"boot entry data is not one BOOTy X.509 signature list", (CHAR8 *)"error payload",
			    EFI_INVALID_PARAMETER);

	s = uefi_call_wrapper(RT->GetVariable, 5, L"MokList", &shim_lock, &attrs, &old_size, NULL);
	if (s == EFI_BUFFER_TOO_SMALL) {
		old = AllocatePool(old_size);
		if (old == NULL)
			return fail(L"allocate", (CHAR8 *)"error alloc", EFI_OUT_OF_RESOURCES);
		s = uefi_call_wrapper(RT->GetVariable, 5, L"MokList", &shim_lock, &attrs, &old_size, old);
	}
	if (s == EFI_NOT_FOUND)
		old_size = 0;
	else if (EFI_ERROR(s))
		return fail(L"read MokList", (CHAR8 *)"error read", s);
	// A MokList with other attributes (the runtime attribute: the OS could
	// have written it) is not trusted: shim deletes it, so do the same and
	// never carry its entries over into an NV|BS MokList.
	if (old_size > 0 && attrs != MOK_ATTRS) {
		Print(L"BOOTy mok-enroll: discarding a MokList with attributes 0x%x\n", attrs);
		s = uefi_call_wrapper(RT->SetVariable, 5, L"MokList", &shim_lock, 0, 0, NULL);
		if (EFI_ERROR(s))
			return fail(L"delete untrusted MokList", (CHAR8 *)"error delete", s);
		old_size = 0;
	}

	s = merge(old, old_size, li->LoadOptions, li->LoadOptionsSize, &merged, &merged_size);
	if (EFI_ERROR(s))
		return fail(L"MokList is malformed, left unchanged", (CHAR8 *)"error malformed", s);
	s = uefi_call_wrapper(RT->SetVariable, 5, L"MokList", &shim_lock, MOK_ATTRS, merged_size, merged);
	if (EFI_ERROR(s))
		return fail(L"write MokList", (CHAR8 *)"error write", s);

	delete_own_entry();
	set_status((CHAR8 *)"ok");
	Print(L"BOOTy mok-enroll: MokList updated (%d bytes), resetting\n", merged_size);
	uefi_call_wrapper(BS->Stall, 1, 2 * 1000 * 1000);
	uefi_call_wrapper(RT->ResetSystem, 4, EfiResetCold, EFI_SUCCESS, 0, NULL);
	return EFI_SUCCESS;
}
